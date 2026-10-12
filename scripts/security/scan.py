"""Scan tracked source and exact release artifacts; keep raw findings private."""
import argparse
from collections import Counter
from datetime import date, datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import sys

ROOT = Path(__file__).resolve().parents[2]
VERSION = '0.75.0'
GOVULNCHECK_VERSION = 'v1.9.0'


def counts(report):
    vulnerabilities, secrets = Counter(), 0
    for result in report.get('Results', []):
        for finding in result.get('Vulnerabilities', []):
            vulnerabilities[finding.get('Severity', 'UNKNOWN')] += 1
        secrets += len(result.get('Secrets', []))
    return dict(sorted(vulnerabilities.items())), secrets


def validate_image(image):
    if not re.fullmatch(r'[a-zA-Z0-9./:_-]+@sha256:[0-9a-f]{64}', image):
        raise ValueError('Published image must use an immutable sha256 digest')
    return image


def govuln_summary(path):
    """Keep symbol-level findings private and export only coverage and counts."""
    text = path.read_text()
    decoder = json.JSONDecoder()
    config, findings, called = None, 0, 0
    while text.strip():
        record, end = decoder.raw_decode(text.lstrip())
        text = text.lstrip()[end:]
        if 'config' in record:
            config = record['config']
        if 'finding' in record:
            findings += 1
            called += any(frame.get('function') for frame in record['finding'].get('trace', []))
    if not config or config.get('scanner_version') != GOVULNCHECK_VERSION or config.get('scan_level') != 'symbol':
        raise ValueError('Missing reviewed Go symbol scan coverage')
    return {'version': GOVULNCHECK_VERSION, 'database_updated_at': config.get('db_last_modified'),
            'findings': findings, 'called_function_findings': called}


def evaluate_policy(reports, policy, exceptions=(), today=None):
    """Every high-severity finding needs remediation or an explicit live exception."""
    today = today or datetime.now(timezone.utc).date()
    approved = set()
    for entry in exceptions:
        if (entry.get('scope') not in {'source', 'image', 'ci-image'} or
                not all(isinstance(entry.get(k), str) and entry[k].strip() for k in
                        ('id', 'package', 'version', 'owner', 'rationale', 'tracking_issue', 'expires'))):
            raise ValueError('Incomplete scoped security exception')
        expiry = date.fromisoformat(entry['expires'])
        if expiry < today or (expiry - today).days > 90:
            raise ValueError('Security exceptions must be current and expire within 90 days')
        approved.add((entry['scope'], entry['id'], entry['package'], entry['version']))
    blocked, exempted, secrets = 0, 0, 0
    for kind, report in reports.items():
        for result in report.get('Results', []):
            secrets += len(result.get('Secrets', []))
            for finding in result.get('Vulnerabilities', []):
                if finding.get('Severity') not in {'HIGH', 'CRITICAL'}:
                    continue
                key = (kind, finding.get('VulnerabilityID'), finding.get('PkgName'), finding.get('InstalledVersion'))
                if key in approved:
                    exempted += 1
                else:
                    blocked += 1
    # A scanner match is not proof a credential works. Hold it for private review;
    # CVE exceptions never suppress secret matches.
    return {'blocked_vulnerabilities': blocked, 'excepted_vulnerabilities': exempted,
            'secret_matches_pending_review': secrets,
            'passed': policy == 'report-only' or not (blocked or secrets)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    image = parser.add_mutually_exclusive_group(required=True)
    image.add_argument('--image', type=validate_image)
    image.add_argument('--image-archive', type=Path)
    ci_image = parser.add_mutually_exclusive_group()
    ci_image.add_argument('--ci-image', type=validate_image)
    ci_image.add_argument('--ci-image-archive', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--trivy', default='trivy')
    parser.add_argument('--policy', choices=('release', 'report-only'), default='release')
    parser.add_argument('--exceptions', type=Path, default=ROOT / 'scripts/security/exceptions.json')
    parser.add_argument('--java-home', type=Path)
    parser.add_argument('--registry-user', help='Read registry password from stdin for a remote image scan')
    parser.add_argument('--source-only', action='store_true', help='Scan source only; record image coverage as absent')
    parser.add_argument('--image-only', action='store_true', help='Scan image only; record source/Android coverage as absent')
    args = parser.parse_args()
    if args.policy == 'release' and (args.source_only or args.image_only):
        parser.error('Release gating requires source and image coverage; use report-only for partial scans')
    if args.source_only and args.image_only:
        parser.error('Choose at most one partial scan mode')
    # Scanner and Gradle logs may contain sensitive matches: never stream them to CI.
    os.umask(0o077)
    output = args.output.resolve()
    if output.is_relative_to(ROOT):
        ignored = subprocess.run(['git', 'check-ignore', '-q', str(output)], cwd=ROOT)
        if ignored.returncode != 0:
            parser.error('Raw reports inside the checkout must use an ignored directory')
    if output.exists() and any(output.iterdir()):
        parser.error('Use a new empty output directory to preserve earlier evidence')
    output.mkdir(parents=True, exist_ok=True, mode=0o700)
    output.chmod(0o700)
    registry_secret = sys.stdin.buffer.read() if args.registry_user else None
    environment = os.environ.copy()
    if args.java_home:
        environment['JAVA_HOME'] = str(args.java_home.resolve())

    def run(command, name, registry_input=False):
        with (output / (name + '.log')).open('w') as log:
            result = subprocess.run(command, cwd=ROOT, env=environment, stdout=log, stderr=log,
                                    stdin=None if registry_input else subprocess.DEVNULL,
                                    input=registry_secret if registry_input else None)
        if result.returncode:
            raise RuntimeError(f'{name} failed; inspect the private log')

    version = json.loads(subprocess.check_output([args.trivy, 'version', '--format', 'json'], text=True))
    if version.get('Version') != VERSION:
        parser.error(f'Reviewed Trivy {VERSION} required')
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    exception_document = json.loads(args.exceptions.read_text())
    if exception_document.get('schema') != 1 or not isinstance(exception_document.get('exceptions'), list):
        parser.error('Invalid exception policy document')
    report_documents = {}
    manifest = {'source_revision': revision, 'source_dirty': bool(subprocess.check_output(['git', 'status', '--porcelain'], cwd=ROOT)),
                'started_at': datetime.now(timezone.utc).isoformat(), 'scanner': version,
                'image': args.image, 'policy': args.policy, 'reports': {}, 'coverage': []}
    base = [args.trivy, '--quiet']
    with tempfile.TemporaryDirectory(prefix='alreadyon-security-') as temporary:
        staging = Path(temporary)
        if not args.image_only:
            # Only tracked files enter the source scan. No live DB, ignored notes or credentials.
            paths = subprocess.check_output(['git', 'ls-files', '-z'], cwd=ROOT).split(b'\0')
            tree = hashlib.sha256()
            for raw in filter(None, paths):
                relative = Path(os.fsdecode(raw))
                source = ROOT / relative
                if source.is_symlink():
                    continue
                if source.is_file():
                    target = staging / relative
                    target.parent.mkdir(parents=True, exist_ok=True)
                    data = source.read_bytes()
                    target.write_bytes(data)
                    tree.update(raw + b'\0' + hashlib.sha256(data).digest())
            manifest['tracked_tree_sha256'] = tree.hexdigest()
            tool_environment = dict(environment, GOBIN=str(staging / 'tools'))
            with (output / 'govulncheck-install.log').open('w') as log:
                installed = subprocess.run(['go', 'install', 'golang.org/x/vuln/cmd/govulncheck@' + GOVULNCHECK_VERSION],
                                           cwd=ROOT / 'server', env=tool_environment, stdout=log, stderr=log,
                                           stdin=subprocess.DEVNULL)
            if installed.returncode:
                raise RuntimeError('Go scanner installation failed; inspect the private log')
            with (output / 'govulncheck.json').open('w') as report, (output / 'govulncheck.log').open('w') as log:
                result = subprocess.run([str(staging / 'tools/govulncheck'), '-json', './...'],
                                        cwd=staging / 'server', env=environment, stdout=report, stderr=log,
                                        stdin=subprocess.DEVNULL)
            if result.returncode not in (0, 3):
                raise RuntimeError('Go symbol scan failed; inspect the private log')
            manifest['govulncheck'] = govuln_summary(output / 'govulncheck.json')
            manifest['coverage'].append('Go server packages: pinned govulncheck symbol analysis; findings are not severity waivers')
            inventory = staging / 'android-dependencies'
            run([str(ROOT / 'apps/android/gradlew'), '-p', str(ROOT / 'apps/android'), '--console=plain',
                 '-I', str(ROOT / 'scripts/security/android-inventory.gradle'),
                 f'-PsecurityInventoryDirectory={inventory}', 'securityInventory'], 'android-inventory')
            manifest['android_modules'] = {p.name: len(p.read_text().splitlines()) - 1 for p in inventory.glob('*gradle.lockfile')}
            manifest['coverage'].append('tracked source: Go/npm including dev dependencies, phone/TV resolved release runtime, secrets')
            run([*base, 'fs', '--scanners', 'vuln,secret', '--include-dev-deps', '--ignorefile', '',
                 '--format', 'json', '--output', str(output / 'source.json'), str(staging)], 'source')
        if not args.source_only:
            image_args = ['--input', str(args.image_archive.resolve())] if args.image_archive else ['--image-src', 'remote', args.image]
            if args.registry_user:
                image_args = ['--username', args.registry_user, '--password-stdin', *image_args]
            if args.image_archive:
                with args.image_archive.open('rb') as archive:
                    manifest['image_archive_sha256'] = hashlib.file_digest(archive, 'sha256').hexdigest()
            run([*base, 'image', '--scanners', 'vuln,secret', '--ignorefile', '', '--format', 'json',
                 '--output', str(output / 'image.json'), *image_args], 'image', registry_input=bool(args.registry_user))
            manifest['coverage'].append('exact image: OS packages, detected bundled libraries/binaries, secrets')
        if args.ci_image or args.ci_image_archive:
            ci_args = ['--input', str(args.ci_image_archive.resolve())] if args.ci_image_archive else ['--image-src', 'remote', args.ci_image]
            if args.registry_user:
                ci_args = ['--username', args.registry_user, '--password-stdin', *ci_args]
            if args.ci_image_archive:
                with args.ci_image_archive.open('rb') as archive:
                    manifest['ci_image_archive_sha256'] = hashlib.file_digest(archive, 'sha256').hexdigest()
            manifest['ci_image'] = args.ci_image
            run([*base, 'image', '--scanners', 'vuln,secret', '--ignorefile', '', '--format', 'json',
                 '--output', str(output / 'ci-image.json'), *ci_args], 'ci-image', registry_input=bool(args.registry_user))
            manifest['coverage'].append('exact CI image: OS packages, compiler and npm tooling, secrets')
        for kind in ('source', 'image', 'ci-image'):
            path = output / (kind + '.json')
            if not path.exists():
                continue
            report = json.loads(path.read_text())
            report_documents[kind] = report
            if kind in {'image', 'ci-image'} and (not report.get('Metadata', {}).get('ImageID') or
                    not any(result.get('Packages') for result in report.get('Results', []))):
                raise RuntimeError('Image scan is missing required artifact/package inventory')
            if kind == 'source':
                detected = {result.get('Type') for result in report.get('Results', [])}
                if not {'gradle', 'gomod', 'npm'} <= detected:
                    raise RuntimeError('Source scan is missing required dependency ecosystems')
            vulnerabilities, secrets = counts(report)
            manifest['reports'][kind] = {'vulnerabilities': vulnerabilities, 'secret_matches': secrets,
                                        'metadata': report.get('Metadata', {}),
                                        'report_sha256': hashlib.sha256(path.read_bytes()).hexdigest()}
            run([*base, 'convert', '--format', 'cyclonedx', '--output', str(output / (kind + '.cdx.json')), str(path)], kind + '-sbom')
    # Include the advisory DB timestamps actually used, not just the CLI version.
    manifest['scanner'] = json.loads(subprocess.check_output([args.trivy, 'version', '--format', 'json'], text=True))
    assessment = evaluate_policy(report_documents, args.policy, exception_document['exceptions'])
    manifest['policy_assessment'] = assessment
    manifest['exceptions_sha256'] = hashlib.sha256(args.exceptions.read_bytes()).hexdigest()
    manifest['completed_at'] = datetime.now(timezone.utc).isoformat()
    (output / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
    summary = {'source_revision': revision, 'source_dirty': manifest['source_dirty'], 'image': args.image,
               'image_archive_sha256': manifest.get('image_archive_sha256'),
               'govulncheck': manifest.get('govulncheck'), 'ci_image': manifest.get('ci_image'), 'ci_image_archive_sha256': manifest.get('ci_image_archive_sha256'), 'policy': args.policy, 'assessment': assessment,
               'reports': {k: {key: value[key] for key in ('vulnerabilities', 'secret_matches')} for k, value in manifest['reports'].items()}}
    (output / 'summary.json').write_text(json.dumps(summary, indent=2) + '\n')
    print(json.dumps(summary, indent=2))
    print('Raw reports, SBOMs and database timestamps remain in the private output directory.')
    if not assessment['passed']:
        raise RuntimeError('Release scan blocked: remediate Critical/High findings and privately review secret matches')


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, subprocess.CalledProcessError) as error:
        raise SystemExit(str(error)) from error
