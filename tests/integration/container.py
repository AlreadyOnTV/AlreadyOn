#!/usr/bin/env python3
"""Test the finished runtime image with disposable volumes, media and credentials.

Only standard-library Python and the selected container CLI are required. No
personal library, host database, fixed host port or persistent test key is used.
"""
import http.client
import http.cookiejar
import ipaddress
import json
import os
import re
import secrets
import ssl
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

ENGINE = os.environ.get('CONTAINER_ENGINE', 'docker')
IMAGE = os.environ.get('CONTAINER_IMAGE', 'alreadyon:local')
NAME = 'alreadyon-integration-' + uuid.uuid4().hex[:12]
VOLUMES = [NAME + '-config', NAME + '-media']


def engine(*args, input=None, check=True):
    """Capture CLI output; callers never pass authentication tokens on argv."""
    result = subprocess.run([ENGINE, *args], input=input, capture_output=True)
    if check and result.returncode:
        raise RuntimeError(f'Container operation {args[0]} failed: {result.stderr.decode()[-2000:]}')
    return result


def wait_for(check, label, seconds=180):
    """Retry startup and asynchronous scanning with a bounded deadline."""
    end = time.monotonic() + seconds
    while time.monotonic() < end:
        if check():
            return
        time.sleep(1)
    raise AssertionError('Timed out: ' + label)


def main():
    """Exercise non-root permissions, real HTTP playback and persisted settings."""
    for volume in VOLUMES:
        engine('volume', 'create', volume)
    # Generate a long-enough synthetic movie for the scanner's duration policy.
    engine('run', '--rm', '--user', '0', '--entrypoint', 'ffmpeg', '-v', VOLUMES[1] + ':/fixture', IMAGE,
           '-nostdin', '-v', 'error', '-f', 'lavfi', '-i', 'testsrc2=size=160x90:rate=10',
           '-f', 'lavfi', '-i', 'sine=frequency=440:sample_rate=48000', '-t', '600',
           '-c:v', 'libopenh264', '-threads', '1', '-g', '20', '-b:v', '120k',
           '-c:a', 'aac', '-ac', '2', '-b:a', '96k', '/fixture/Synthetic Fixture (2020).mp4')
    engine('run', '-d', '--name', NAME, '-e', 'PUID=1000', '-e', 'PGID=1000',
           '-e', 'TZ=America/New_York', '-p', '127.0.0.1::19900',
           '-v', VOLUMES[0] + ':/config', '-v', VOLUMES[1] + ':/fixture:ro', IMAGE)
    management = 'https://' + engine('port', NAME, '19900/tcp').stdout.decode().strip()
    media = management  # Token-scoped media shares the application's HTTPS origin.
    # Read only the public CA through the trusted engine; normal TLS hostname
    # verification remains enabled on every management and media request.
    trust = None
    def load_trust():
        nonlocal trust
        result = engine('exec', '--user', '1000:1000', NAME, 'cat', '/config/tls/ca.pem', check=False)
        if result.returncode:
            return False
        trust = ssl.create_default_context(cadata=result.stdout.decode())
        return True
    wait_for(load_trust, 'instance public CA')
    initial_ca = engine('exec', '--user', '1000:1000', NAME, 'cat', '/config/tls/ca.pem').stdout
    jar = http.cookiejar.CookieJar()
    client = urllib.request.build_opener(urllib.request.HTTPSHandler(context=trust), urllib.request.HTTPCookieProcessor(jar))

    def request(method, path, body=None, authenticated=True, base=None):
        payload = None if body is None else json.dumps(body).encode()
        url = (base or management) + path
        req = urllib.request.Request(url, data=payload, method=method,
                                     headers={'Content-Type': 'application/json'})
        try:
            with (client.open(req, timeout=60) if authenticated else urllib.request.urlopen(req, timeout=60, context=trust)) as response:
                return response.status, response.read()
        except urllib.error.HTTPError as error:
            return error.code, error.read()
        except (urllib.error.URLError, OSError, http.client.HTTPException):
            return 0, b''  # No URLs/tokens in failure output.

    def api(method, path, body=None, expected=200):
        status, data = request(method, path, body)
        assert status == expected, f'{method} {path.split("?")[0]}: HTTP {status}, expected {expected}'
        return json.loads(data) if data else None

    def health():
        return request('GET', '/healthz', authenticated=False)[0] == 200

    wait_for(health, 'management startup')
    status = engine('exec', NAME, 'cat', '/proc/1/status').stdout.decode()
    assert 'Uid:\t1000\t1000\t1000\t1000' in status, 'Server must run as PUID 1000'
    assert 'Gid:\t1000\t1000\t1000\t1000' in status, 'Server must run as PGID 1000'
    engine('exec', '--user', '1000:1000', NAME, 'sh', '-c', 'test -w /config && touch /config/permission-test')
    assert engine('exec', '--user', '0', NAME, 'touch', '/fixture/must-not-write', check=False).returncode != 0
    assert request('GET', '/api/channels', authenticated=False)[0] == 401
    assert request('GET', '/media/999/index.m3u8', authenticated=False, base=media)[0] == 401
    assert request('GET', '/api/channels', authenticated=False, base=media)[0] in (401, 404)
    assert request('GET', '/', authenticated=False)[0] == 200
    print('PASS: non-root runtime, writable config, read-only media and shared-listener authentication boundaries', flush=True)

    credentials = {'username': 'ci-user', 'password': secrets.token_urlsafe(24)}
    api('POST', '/api/auth/setup', credentials, 201)
    # The laptop deploy wrapper uses this packaged utility after health checks.
    # It must revoke only browser auth; signing back in proves the account remains.
    engine('exec', '--user', '1000:1000', NAME, 'alreadyon-clear-browser-sessions')
    assert request('GET', '/api/server/details')[0] == 401
    api('POST', '/api/auth/login', credentials)
    assert request('GET', '/api/server/details', authenticated=False)[0] == 401
    details = api('GET', '/api/server/details')
    assert re.fullmatch(r'\d{2}\.\d{2}\.\d{2}_[0-9a-f]{7}', details['appVersion']), details['appVersion']
    revision = engine('image', 'inspect', '--format', '{{index .Config.Labels "org.opencontainers.image.revision"}}', IMAGE).stdout.decode().strip()
    assert details['appVersion'].endswith('_' + revision[:7]), 'Build stamp must identify the image revision'
    # Client identity must survive the real container build context, not merely
    # a local Vite build. Inspect the served assets rather than the server stamp.
    code, index = request('GET', '/', authenticated=False)
    scripts = re.findall(r'<script[^>]*src="([^"]+)"', index.decode())
    assert code == 200 and scripts, 'WebUI scripts missing'
    bundles = []
    for path in scripts:
        code, bundle = request('GET', path, authenticated=False)
        assert code == 200, 'WebUI bundle unavailable'
        bundles.append(bundle.decode())
    assert any(re.search(r'\d{2}\.\d{2}\.\d{2}_' + revision[:7], bundle) for bundle in bundles), 'WebUI client version must identify its bundled image revision'

    assert details['uptimeSeconds'] >= 0 and details['timeZone'] == 'America/New_York'
    assert details['ffmpegVersion'] != 'Unavailable'
    assert all(ipaddress.ip_address(address).version == 4 for address in details['ipAddresses'])
    print('PASS: authenticated build identity, process uptime, time zone and installed FFmpeg version', flush=True)
    # Settings saves regenerate schedules through the background governor. Wait
    # before fixture mutations too: startup probes can otherwise stall a PUT
    # beyond the HTTP client deadline, before the old playback-only wait.
    wait_for(lambda: api('GET', '/api/settings/transcoding/threads').get('state') == 'idle', 'thread benchmark')
    # The optional HDR matrix deliberately remains part of the capacity run and
    # has a ten-minute server-side deadline. Two-CPU hosted runners can need
    # longer than the generic three-minute asynchronous-work allowance.
    wait_for(lambda: api('GET', '/api/settings/transcoding/capacity').get('state') == 'idle',
             'capacity benchmark', 660)
    settings = api('GET', '/api/settings/commercials')
    settings['enabled'] = False
    api('PUT', '/api/settings/commercials', settings)
    api('PUT', '/api/settings/folders', {'tv': [], 'movies': ['/fixture'], 'commercials': [], 'exclusions': []})
    wait_for(lambda: api('GET', '/api/settings/folders')['movies']['paths'][0].get('count', 0) == 1,
             'synthetic movie scan')
    channel = api('POST', '/api/channels', {'number': 9001, 'name': 'Synthetic Integration',
                  'filter': {'audienceTier': 'adult', 'genres': [], 'mediaType': 'movie',
                             'era': 'both', 'titleContains': 'Synthetic Fixture'}}, 201)
    assert channel['itemCount'] == 1, 'Synthetic movie must be in the channel'
    channel_id = channel['id']
    capabilities = {'hls': True, 'containers': ['mp4'], 'mbps': 25,
                    'video': [{'codec': 'h264', 'profile': 'High', 'level': 42, 'width': 1920,
                               'height': 1080, 'fps': 60, 'bitrate': 20000000, 'pixelFormat': 'yuv420p'}],
                    'audio': [{'codec': 'aac', 'channels': 2, 'sampleRate': 48000, 'bitrate': 320000}]}
    def tune():
        result = None

        def admitted():
            nonlocal result
            status, data = request('POST', f'/api/channels/{channel_id}/tune',
                                   {'capabilities': capabilities, 'forceEncode': True})
            value = json.loads(data) if data else {}
            # A released worker can still be exiting. Honor the API's bounded
            # capacity retry rather than assuming a conversion slot is instant.
            if status == 503 and value.get('code') == 'stream_capacity':
                return False
            assert status == 201, f'Tune failed with HTTP {status}'
            result = value
            return True

        wait_for(admitted, 'conversion admission', 30)
        return result

    tuned = tune()
    assert tuned['plan']['mode'] == 'transcode', 'Must exercise software conversion, not direct play'
    parts = urllib.parse.urlsplit(tuned['url'])
    manifest_path = parts.path + '?' + parts.query
    manifest = b''

    def ready():
        nonlocal manifest
        status, manifest = request('GET', manifest_path, authenticated=False, base=media)
        return status == 200 and b'#EXTINF' in manifest

    wait_for(ready, 'HLS output', 90)
    segment = next(line for line in manifest.decode().splitlines() if line and not line.startswith('#'))
    segment_parts = urllib.parse.urlsplit(urllib.parse.urljoin(media + manifest_path, segment))
    code, content = request('GET', segment_parts.path + '?' + segment_parts.query, authenticated=False, base=media)
    assert code == 200 and len(content) > 0, 'Authorized media segment unavailable'
    # Decode the delivered bytes with the bundled FFmpeg; no credential is logged.
    engine('exec', '-i', NAME, 'ffmpeg', '-v', 'error', '-i', 'pipe:0', '-f', 'null', '-', input=content)
    print('PASS: authenticated software HLS session and real segment decoding', flush=True)
    api('DELETE', '/api/playback/sessions', {'token': tuned['token'], 'channelId': channel_id}, 204)
    assert request('GET', manifest_path, authenticated=False, base=media)[0] == 401
    # Stop with an active session as well, proving child-process shutdown cleanup.
    tuned = tune()
    active_parts = urllib.parse.urlsplit(tuned['url'])
    restart_path = active_parts.path + '?' + active_parts.query
    engine('stop', '--time', '20', NAME)
    exit_code = json.loads(engine('inspect', NAME).stdout)[0]['State']['ExitCode']
    assert exit_code == 0, 'Graceful stop must not require SIGKILL'
    engine('start', NAME)
    # Docker may reassign ephemeral host ports across stop/start; discover again.
    management = 'https://' + engine('port', NAME, '19900/tcp').stdout.decode().strip()
    media = management  # Token-scoped media shares the application's HTTPS origin.
    wait_for(health, 'restart')
    assert engine('exec', '--user', '1000:1000', NAME, 'cat', '/config/tls/ca.pem').stdout == initial_ca, 'Restart changed instance trust'
    engine('exec', '--user', '1000:1000', NAME, 'alreadyon-tls', 'health')
    api('POST', '/api/auth/login', credentials)
    assert api('GET', '/api/settings/commercials')['enabled'] is False
    assert api('GET', f'/api/channels/{channel_id}')['name'] == 'Synthetic Integration'
    assert api('GET', '/api/settings/folders')['movies']['paths'][0]['path'] == '/fixture'
    assert request('GET', restart_path, authenticated=False, base=media)[0] == 401
    # No persisted HLS output from the stopped process may remain usable.
    output = engine('exec', NAME, 'find', '/config/transcode', '-name', '*.m3u8').stdout
    assert not output.strip(), 'Stale stream output survived restart'
    engine('exec', NAME, 'alreadyon-check-media-stack')
    print('PASS: authorization revocation, graceful stop, restart persistence and stream cleanup', flush=True)


if __name__ == '__main__':
    try:
        main()
    finally:
        engine('rm', '-f', NAME, check=False)
        for volume in VOLUMES:
            engine('volume', 'rm', volume, check=False)
