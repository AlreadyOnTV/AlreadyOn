#!/usr/bin/env python3
"""Retry Android builds only when a dependency repository reports HTTP 429."""
import re
import subprocess
import sys
import time


def run(command):
    """Keep genuine build failures fatal; throttle retries stop after three tries."""
    for attempt in range(3):
        throttled = False
        with subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                              text=True, bufsize=1) as process:
            for line in process.stdout:
                print(line, end='', flush=True)
                throttled |= bool(re.search(r'Received status code 429 from server:', line))
            code = process.wait()
        if code == 0 or not throttled or attempt == 2:
            return code
        delay = 20 * (attempt + 1)
        print(f'Dependency repository throttled requests; retrying in {delay}s.', flush=True)
        time.sleep(delay)


if __name__ == '__main__':
    if len(sys.argv) < 2:
        sys.exit('Usage: retry-android.py COMMAND [ARG ...]')
    sys.exit(run(sys.argv[1:]))
