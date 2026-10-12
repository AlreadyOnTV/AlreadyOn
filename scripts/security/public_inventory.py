"""Create inventory-only CycloneDX files bound to exact images and signed APKs."""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
from urllib.parse import quote
import uuid

from scan import VERSION, validate_image

ROOT = Path(__file__).resolve().parents[2]
NAMES = ('sbom-image.cdx.json', 'sbom-android.cdx.json', 'sbom-tv.cdx.json')


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def package_component(name, version, purl, licenses=()):
    # Reconstruct an allowlisted inventory rather than exposing scanner report fields.
    if name.startswith('/') or any(part in name for part in ('://', '/home/', '/tmp/', '/config/')):
        raise ValueError('Non-package path in public inventory')
    if not all(isinstance(s, str) and s and not re.search(r'[\s\x00-\x1f]', s) for s in (name, version, purl)):
        raise ValueError('Invalid public package identity')
    if not re.fullmatch(r'pkg:(deb|golang|npm|maven|generic)/[A-Za-z0-9._~%/@+?=&:-]+', purl):
        raise ValueError('Unsupported public package URL')
    component = {'type': 'library', 'bom-ref': purl, 'name': name, 'version': version, 'purl': purl}
    safe_licenses = sorted({s for s in licenses if isinstance(s, str) and re.fullmatch(r'[A-Za-z0-9.+-]+', s)})
    if safe_licenses:
        component['licenses'] = [{'license': {'name': s}} for s in safe_licenses]
    return component


def image_components(report):
    packages = {}
    for result in report.get('Results', []):
        for package in result.get('Packages', []):
            purl = package.get('Identifier', {}).get('PURL')
            if not purl or not package.get('Version'):
                continue
            component = package_component(package['Name'], package['Version'], purl, package.get('Licenses', []))
            packages[purl] = component
    if not packages:
        raise ValueError('Image inventory contains no identified packages')
    return [packages[key] for key in sorted(packages)]


def android_components(lockfile):
    components = []
    for line in lockfile.read_text().splitlines():
        if line == 'empty=' or line.startswith('#') or not line:
            continue
        coordinate, configuration = line.split('=', 1)
        if configuration != 'releaseRuntimeClasspath':
            raise ValueError('Unexpected Android dependency scope')
        group, name, version = coordinate.split(':')
        components.append(package_component(name, version, f'pkg:maven/{quote(group, safe="")}/{quote(name, safe="")}@{quote(version, safe="")}'))
    if not components:
        raise ValueError('Android inventory is empty')
    return components


def inventory(subject, sha256, revision, components, coverage):
    if not re.fullmatch(r'[0-9a-f]{40}', revision) or not re.fullmatch(r'[0-9a-f]{64}', sha256):
        raise ValueError('Invalid artifact identity')
    return {'bomFormat': 'CycloneDX', 'specVersion': '1.6', 'version': 1,
            'serialNumber': 'urn:uuid:' + str(uuid.uuid4()),
            'metadata': {'timestamp': datetime.now(timezone.utc).isoformat(),
                         'tools': {'components': [{'type': 'application', 'name': 'Trivy', 'version': VERSION},
                                                  {'type': 'application', 'name': 'AlreadyOn inventory exporter', 'version': '1'}]},
                         'component': {'type': 'application', 'name': subject, 'version': revision,
                                       'hashes': [{'alg': 'SHA-256', 'content': sha256}]},
                         'properties': [{'name': 'alreadyon:source_revision', 'value': revision},
                                        {'name': 'alreadyon:coverage', 'value': coverage}]},
            'components': components}


def verify_bundle(directory, revision, image=None, phone=None, tv=None):
    manifest = json.loads((directory / 'sbom-manifest.json').read_text())
    if manifest.get('source_revision') != revision or manifest.get('schema') != 1:
        raise ValueError('SBOM source provenance mismatch')
    if set(manifest.get('files', {})) != set(NAMES):
        raise ValueError('Incomplete SBOM inventory bundle')
    for name, checksum in manifest['files'].items():
        if digest(directory / name) != checksum:
            raise ValueError('SBOM checksum mismatch')
        bom = json.loads((directory / name).read_text())
        if bom.get('bomFormat') != 'CycloneDX' or 'vulnerabilities' in bom:
            raise ValueError('Expected inventory-only CycloneDX')
        subject = bom['metadata']['component']
        expected = manifest['subjects'][name]
        if subject.get('name') != expected['name'] or subject.get('hashes') != [{'alg': 'SHA-256', 'content': expected['sha256']}]:
            raise ValueError('SBOM subject identity mismatch')
        if subject.get('version') != revision:
            raise ValueError('SBOM revision mismatch')
    for name, path in ((NAMES[1], phone), (NAMES[2], tv)):
        if path and manifest['subjects'][name]['sha256'] != digest(path):
            raise ValueError('SBOM APK subject mismatch')
    if image and manifest['subjects'][NAMES[0]]['name'] != validate_image(image):
        raise ValueError('SBOM image subject mismatch')
    return manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', type=validate_image, required=True)
    parser.add_argument('--phone', type=Path, required=True)
    parser.add_argument('--tv', type=Path, required=True)
    parser.add_argument('--revision', required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--trivy', default='trivy')
    parser.add_argument('--registry-user', help='Registry password is read from stdin')
    args = parser.parse_args()
    head = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    if head != args.revision or subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT):
        parser.error("Public inventory requires a clean checkout of the exact release source")
    if (args.phone.name, args.tv.name) != ("alreadyon-android.apk", "alreadyon-tv.apk"):
        parser.error("Expected the signed release APK filenames")
    args.output.mkdir(parents=True, exist_ok=True)
    if any((args.output / name).exists() for name in (*NAMES, 'sbom-manifest.json')):
        parser.error('Refusing to replace existing inventories')
    version = json.loads(subprocess.check_output([args.trivy, 'version', '--format', 'json'], text=True))
    if version.get('Version') != VERSION:
        parser.error('Reviewed scanner version required')
    with tempfile.TemporaryDirectory(prefix='alreadyon-inventory-') as temporary:
        staging = Path(temporary)
        log = staging / 'scanner.log'
        report = staging / 'image.json'
        credentials = ['--username', args.registry_user, '--password-stdin'] if args.registry_user else []
        with log.open('w') as stream:
            result = subprocess.run([args.trivy, '--quiet', 'image', '--image-src', 'remote', *credentials,
                                     '--scanners', 'license', '--list-all-pkgs', '--format', 'json',
                                     '--output', str(report), args.image], stdout=stream, stderr=stream,
                                    stdin=None if credentials else subprocess.DEVNULL)
            if result.returncode:
                raise RuntimeError('Private inventory scan failed; no inventory published')
            subprocess.run([str(ROOT / 'apps/android/gradlew'), '-p', str(ROOT / 'apps/android'), '--console=plain',
                            '-I', str(ROOT / 'scripts/security/android-inventory.gradle'),
                            f'-PsecurityInventoryDirectory={staging / "android"}', 'securityInventory'],
                           stdout=stream, stderr=stream, check=True, stdin=subprocess.DEVNULL)
        raw_image = json.loads(report.read_text())
        labels = raw_image.get('Metadata', {}).get('ImageConfig', {}).get('config', {}).get('Labels', {})
        if labels.get('org.opencontainers.image.revision') != args.revision:
            raise ValueError('Scanned image does not match the release source')
        manifest = {'schema': 1, 'source_revision': args.revision, 'files': {}, 'subjects': {}}
        subjects = [(NAMES[0], args.image, args.image.rsplit('sha256:', 1)[1], image_components(json.loads(report.read_text())),
                     'Detected OS and bundled packages; custom FFmpeg/codec source dependencies need separate inventory.'),
                    (NAMES[1], args.phone.name, digest(args.phone), android_components(staging / 'android/app-gradle.lockfile'),
                     'Resolved phone release runtime dependencies; R8 may remove code. Build plugins and custom/native source are outside this inventory.'),
                    (NAMES[2], args.tv.name, digest(args.tv), android_components(staging / 'android/tv-gradle.lockfile'),
                     'Resolved TV release runtime dependencies; R8 may remove code. Build plugins and custom/native source are outside this inventory.')]
        for name, subject, checksum, components, coverage in subjects:
            (args.output / name).write_text(json.dumps(inventory(subject, checksum, args.revision, components, coverage), indent=2) + '\n')
            manifest['files'][name] = digest(args.output / name)
            manifest['subjects'][name] = {'name': subject, 'sha256': checksum}
        (args.output / 'sbom-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
        verify_bundle(args.output, args.revision, args.image, args.phone, args.tv)
    print('Verified public inventory bundle: exact image digest and signed phone/TV APK hashes.')


if __name__ == '__main__':
    main()
