#!/usr/bin/env python3
"""Check the shared Dockerfile baseline, Go module and running tools."""
import argparse
import os
import pathlib
import re
import subprocess

root = pathlib.Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser()
parser.add_argument('--installed', action='store_true')
parser.add_argument('--expect-ubuntu', help='Fail if the shared Ubuntu baseline differs')
parser.add_argument('--check-latest-lts', action='store_true',
                    help='Fail if the baseline differs from the Ubuntu release ubuntu:latest resolves to (needs a container engine)')
parser.add_argument('--latest-version', help='Use this as the ubuntu:latest release instead of asking the engine (for tests)')
args = parser.parse_args()
dockerfile = (root / 'deploy/Dockerfile').read_text()
base_dockerfile = (root / 'deploy/base.Dockerfile').read_text()
ci_dockerfile = (root / 'deploy/ci-base.Dockerfile').read_text()
versions = dict(re.findall(r'^ARG (GO_VERSION|NODE_VERSION|UBUNTU_VERSION)=(\S+)$', dockerfile, re.M))
assert len(versions) == 3, 'Missing shared toolchain defaults'
base_versions = dict(re.findall(r'^ARG (GO_VERSION|NODE_VERSION|UBUNTU_VERSION)=(\S+)$', base_dockerfile, re.M))
assert base_versions == {'UBUNTU_VERSION': versions['UBUNTU_VERSION']}, 'Runtime Ubuntu baseline differs'
assert versions == dict(re.findall(r'^ARG (GO_VERSION|NODE_VERSION|UBUNTU_VERSION)=(\S+)$', ci_dockerfile, re.M)), 'CI toolchain defaults differ'
if args.expect_ubuntu:
    assert versions['UBUNTU_VERSION'] == args.expect_ubuntu, 'Ubuntu baseline mismatch'
if args.check_latest_lts:
    latest = args.latest_version
    if not latest:
        engine = os.environ.get('CONTAINER_ENGINE', 'docker')
        out = subprocess.check_output([engine, 'run', '--rm', '--pull=always', 'ubuntu:latest', 'sh', '-c', '. /etc/os-release; echo $VERSION_ID'], text=True)
        latest = out.strip().splitlines()[-1]
    assert versions['UBUNTU_VERSION'] == latest, (
        f'ubuntu:latest is now {latest} but deploy/Dockerfile pins UBUNTU_VERSION={versions["UBUNTU_VERSION"]}: '
        'update the shared production and CI baseline deliberately in this cycle')
    print(f'Ubuntu baseline {latest} matches ubuntu:latest')
go = re.search(r'^go (\S+)', (root / 'server/go.mod').read_text(), re.M)[1]
assert versions['GO_VERSION'] == go, 'Go image and go.mod differ'
assert re.findall(r'^FROM (.+)$', dockerfile, re.M)[-1] == '${RUNTIME_BASE} AS runtime', 'Default image must remain the production runtime'
assert 'FROM ci-base AS server-build' in dockerfile
assert 'FROM ci-base AS web-build' in dockerfile
assert 'COPY --from=go-toolchain /usr/local/go /usr/local/go' in ci_dockerfile
assert 'COPY --from=node-toolchain /usr/local/ /usr/local/' in ci_dockerfile
assert 'FROM ${RUNTIME_BASE} AS ci-base' in ci_dockerfile
ubuntu_stages = re.findall(r'^FROM ubuntu:(.+?)\s', base_dockerfile, re.M)
assert len(ubuntu_stages) >= 2 and set(ubuntu_stages) == {'${UBUNTU_VERSION}'}, 'Ubuntu stages must share their version'
if args.installed:
    actual_go = subprocess.check_output(['go', 'version'], text=True).split()[2][2:]
    actual_node = subprocess.check_output(['node', '--version'], text=True).strip().lstrip('v')
    assert actual_go.startswith(go + '.'), f'Expected Go {go}.x; got {actual_go}'
    assert actual_node.split('.')[0] == versions['NODE_VERSION'], f'Unexpected Node {actual_node}'
    release = pathlib.Path('/etc/os-release')
    if pathlib.Path('/opt/playwright').exists():
        assert f'VERSION_ID="{versions["UBUNTU_VERSION"]}"' in release.read_text(), 'Ubuntu baseline drift'
print('Shared toolchain baseline OK:', ', '.join(f'{k}={v}' for k, v in versions.items()))
