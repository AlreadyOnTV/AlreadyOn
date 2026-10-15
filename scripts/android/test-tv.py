#!/usr/bin/env python3
"""Run TV onboarding/navigation on an emulator using a disposable HTTPS/media fixture."""
import argparse
import http.cookiejar
import json
import os
from pathlib import Path
import socket
import sqlite3
import ssl
import subprocess
import tempfile
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[2]


def run(*args, cwd=ROOT, capture=False):
    result = subprocess.run(args, cwd=cwd, check=True, text=True,
                            stdout=subprocess.PIPE if capture else None)
    return result.stdout if capture else ""


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--serial", required=True)
    parser.add_argument("--sdk", required=True, type=Path)
    parser.add_argument("--screenshots", type=Path, help="Optional local directory for emulator screenshots")
    parser.add_argument("--entry-mode", action="append", choices=("discovery", "manual"), help="Limit to selected connection flow; default runs both")
    parser.add_argument("--test-class", choices=("TvNavigationTest", "ProfileMenuTest", "SessionResumeTest"), default="TvNavigationTest", help="Select native navigation or focused profile-menu coverage")
    parser.add_argument("--interactive", action="store_true", help="Leave the disposable server running for manual emulator checks until Enter is pressed")
    args = parser.parse_args()
    adb = str(args.sdk / "platform-tools/adb")
    if not args.serial.startswith("emulator-") or run(adb, "-s", args.serial, "shell", "getprop", "ro.kernel.qemu", capture=True).strip() != "1":
        parser.error("Only an explicitly selected emulator may run this disposable test")
    print("This test clears only the selected emulator's AlreadyOn TV debug app data.")
    run(str(ROOT / "apps/android/gradlew"), "-p", str(ROOT / "apps/android"),
        ":tv:assembleDebug", ":tv:assembleDebugAndroidTest")
    with tempfile.TemporaryDirectory(prefix="alreadyon-tv-test.") as temp:
        root = Path(temp)
        data = root / "data"
        media = root / "media"
        media.mkdir()
        binary = root / "server"
        # Compile private paths into this disposable binary, never into repository configuration.
        run("go", "build", "-o", str(binary), "-ldflags",
            f"-X github.com/alreadyonapp/alreadyon/server/internal/config.dataDir={data} "
            f"-X github.com/alreadyonapp/alreadyon/server/internal/config.webDir={ROOT / 'web/dist'}",
            "./cmd/server", cwd=ROOT / "server")
        with socket.socket() as sock:
            # Permit stale fixture TIME_WAIT sockets; an active listener still fails this bind.
            # Never replace a live service to make a synthetic test pass.
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            sock.bind(("127.0.0.1", 19900 if args.test_class == "TvNavigationTest" else 0))
            port = sock.getsockname()[1]
        with (root / "server.log").open("w") as log:
            process = subprocess.Popen([str(binary)], cwd=root,
                env=dict(os.environ, ALREADYON_WEB_PORT=str(port)), stdout=log, stderr=log)
            try:
                deadline = time.monotonic() + 30
                ca = data / "tls/ca.pem"
                while not ca.exists():
                    if process.poll() is not None or time.monotonic() > deadline:
                        raise RuntimeError("Disposable server failed to start")
                    time.sleep(0.1)
                context = ssl.create_default_context(cafile=str(ca))
                base = f"https://localhost:{port}"
                opener = urllib.request.build_opener(urllib.request.HTTPSHandler(context=context),
                    urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
                while True:
                    try:
                        opener.open(base + "/healthz", timeout=2).close()
                        break
                    except OSError:
                        if time.monotonic() > deadline:
                            raise RuntimeError("Disposable server failed health check")
                        time.sleep(0.1)
                def api(path, body, method="POST"):
                    request = urllib.request.Request(base + path, json.dumps(body).encode(),
                        {"Content-Type": "application/json"}, method=method)
                    with opener.open(request) as response:
                        return response.read()
                api("/api/auth/setup", {"username": "tv-fixture", "password": "synthetic-tv-fixture-pass", "serverName": "AlreadyOn TV fixture"})
                api("/api/auth/tv-pin", {"pin": "123456"}, "PUT")
                video = media / "program.mp4"
                run("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i",
                    "testsrc2=size=320x180:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
                    "-t", "600", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
                    "-c:a", "aac", "-movflags", "+faststart", str(video))
                with sqlite3.connect(data / "alreadyon.db") as db:
                    now = int(time.time())
                    db.execute("UPDATE folder_settings SET tv_paths=?,movie_paths='[]',commercial_paths='[]' WHERE id=1", (json.dumps([str(media)]),))
                    db.execute("UPDATE channels SET hidden=1,visibility='hide'")
                    db.execute("INSERT INTO media_files(id,root_kind,path,size_bytes,modified_unix,first_seen_unix,last_seen_unix,duration_seconds,status) VALUES(999,'media',?,?,?,0,0,600,'ok')", (str(video), video.stat().st_size, now))
                    db.execute("INSERT INTO identified_items(id,media_file_id,media_type,title,is_manual_correction) VALUES(999,999,'movie','Synthetic TV Program',1)")
                    for number in (110, 111):
                        channel = db.execute("SELECT id FROM channels WHERE number=?", (number,)).fetchone()[0]
                        db.execute("UPDATE channels SET hidden=0,visibility='show',name=? WHERE id=?", (f"TV fixture {number}", channel))
                        db.execute("DELETE FROM schedule_entries WHERE channel_id=?", (channel,))
                        # Repeat the ten-minute synthetic clip so interactive checks stay playable.
                        for period in range(12):
                            start = now - 10 + period * 600
                            db.execute("INSERT INTO schedule_entries(channel_id,media_file_id,kind,source_offset_seconds,duration_seconds,starts_at_unix,ends_at_unix) VALUES(?,999,'program',0,600,?,?)", (channel, start, start + 600))
                for apk in ("debug/tv-debug.apk", "androidTest/debug/tv-debug-androidTest.apk"):
                    run(adb, "-s", args.serial, "install", "-r", str(ROOT / "apps/android/tv/build/outputs/apk" / apk))
                if args.interactive:
                    run(adb, "-s", args.serial, "shell", "pm", "clear", "io.github.alreadyonapp.alreadyon.tv.debug")
                    run(adb, "-s", args.serial, "shell", "am", "start", "-n", "io.github.alreadyonapp.alreadyon.tv.debug/io.github.alreadyonapp.alreadyon.MainActivity")
                    print(f"Manual emulator address after 30 seconds: https://10.0.2.2:{port}", flush=True)
                    print("Synthetic username: tv-fixture; TV PIN: 123456. The app accepts the server certificate automatically.", flush=True)
                    import hashlib
                    fingerprint = hashlib.sha256(ssl.PEM_cert_to_DER_cert(ca.read_text())).hexdigest()
                    print(f"Expected certificate fingerprint: {fingerprint}", flush=True)
                    print(f"Fixture WebUI: {base}; password only for WebUI: synthetic-tv-fixture-pass", flush=True)
                    input("Press Enter to stop the disposable fixture: ")
                    return
                for entry_mode in args.entry_mode or ("discovery", "manual"):
                    run(adb, "-s", args.serial, "shell", "pm", "clear", "io.github.alreadyonapp.alreadyon.tv.debug")
                    result = run(adb, "-s", args.serial, "shell", "am", "instrument", "-w", "-e", "serverPort", str(port),
                        "-e", "entryMode", entry_mode, "-e", "class", "io.github.alreadyonapp.alreadyon." + args.test_class,
                        "io.github.alreadyonapp.alreadyon.tv.debug.test/androidx.test.runner.AndroidJUnitRunner", capture=True)
                    print(result)
                    if "OK (1 test)" not in result:
                        if args.screenshots:
                            args.screenshots.mkdir(parents=True, exist_ok=True)
                            run(adb, "-s", args.serial, "pull", "/sdcard/Android/data/io.github.alreadyonapp.alreadyon.tv.debug/files/tv-last-state.png", str(args.screenshots / f"tv-failed-{entry_mode}.png"))
                        raise RuntimeError("TV instrumentation did not pass")
                    if args.screenshots:
                        args.screenshots.mkdir(parents=True, exist_ok=True)
                        run(adb, "-s", args.serial, "pull", "/sdcard/Android/data/io.github.alreadyonapp.alreadyon.tv.debug/files/tv-guide.png", str(args.screenshots / f"tv-guide-{entry_mode}.png"))
            finally:
                process.terminate()
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill(); process.wait()
                # Saved sessions must not outlive the disposable fixture they identify.
                run(adb, "-s", args.serial, "shell", "pm", "clear", "io.github.alreadyonapp.alreadyon.tv.debug")


if __name__ == "__main__":
    main()
