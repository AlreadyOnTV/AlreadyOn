"""Install the reviewed Linux amd64 scanner into a caller-owned directory."""
import argparse
import hashlib
import io
from pathlib import Path
import tarfile
import urllib.request

VERSION = '0.75.0'
SHA256 = 'c6e65abddb348e25f10549df887045629cf28cc72453cd1c63acb717316b3f3f'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('directory', type=Path)
    args = parser.parse_args()
    url = f'https://github.com/aquasecurity/trivy/releases/download/v{VERSION}/trivy_{VERSION}_Linux-64bit.tar.gz'
    with urllib.request.urlopen(url, timeout=60) as response:
        archive = response.read()
    if hashlib.sha256(archive).hexdigest() != SHA256:
        raise SystemExit('Scanner archive checksum mismatch')
    with tarfile.open(fileobj=io.BytesIO(archive)) as bundle:
        member = bundle.getmember('trivy')
        if not member.isfile():
            raise SystemExit('Scanner archive has no regular executable')
        data = bundle.extractfile(member).read()
    args.directory.mkdir(parents=True, exist_ok=True)
    target = args.directory / 'trivy'
    target.write_bytes(data)
    target.chmod(0o755)
    print(target)


if __name__ == '__main__':
    main()
