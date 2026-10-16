#!/usr/bin/env python3
"""Exercise updater policy, signed synthetic APKs and dialogs on a selected emulator."""
import argparse
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import re
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[2]
PACKAGE = "io.github.alreadyonapp.alreadyon.updatertest.debug"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--serial", required=True)
    parser.add_argument("--sdk", required=True, type=Path)
    parser.add_argument("--java-home", type=Path)
    parser.add_argument("--screenshots", type=Path)
    args = parser.parse_args()
    environment = os.environ.copy()
    if args.java_home:
        environment["JAVA_HOME"] = str(args.java_home)
    adb = [str(args.sdk / "platform-tools/adb"), "-s", args.serial]

    def run(*command, capture=False):
        return subprocess.run(command, cwd=ROOT, env=environment, check=True, text=True,
                              stdout=subprocess.PIPE if capture else None).stdout

    if run(*adb, "shell", "getprop", "ro.kernel.qemu", capture=True).strip() != "1":
        raise SystemExit("Updater fixture tests require an emulator; physical devices use manual acceptance checks.")
    if run(*adb, "shell", "pm", "list", "packages", PACKAGE, capture=True).strip():
        raise SystemExit("Synthetic updater package already exists; preserve it and select a clean test emulator.")
    gradle = [str(ROOT / "apps/android/gradlew"), "-p", str(ROOT / "apps/android"), "--console=plain",
              "-PandroidDebugSuffix=.updatertest.debug"]
    with tempfile.TemporaryDirectory(prefix="alreadyon-updater-") as temporary:
        assets = Path(temporary)

        def verify_install_cycle():
            run(*adb, "shell", "appops", "set", PACKAGE, "REQUEST_INSTALL_PACKAGES", "allow")
            output = run(*adb, "shell", "am", "instrument", "-w", "-r", "-e", "class",
                         "io.github.alreadyonapp.alreadyon.UpdateInstallationFixture",
                         f"{PACKAGE}.test/androidx.test.runner.AndroidJUnitRunner", capture=True)
            print(output)
            if "OK (1 test)" not in output:
                raise SystemExit("Synthetic installation fixture failed")
            run(*adb, "shell", "am", "start", "-n", f"{PACKAGE}/io.github.alreadyonapp.alreadyon.MainActivity")
            confirmed = False
            for _ in range(30):
                details = run(*adb, "shell", "dumpsys", "package", PACKAGE, capture=True)
                if "versionCode=24 " in details:
                    break
                run(*adb, "shell", "uiautomator", "dump", "/sdcard/alreadyon-update-test.xml", capture=True)
                xml = run(*adb, "shell", "cat", "/sdcard/alreadyon-update-test.xml", capture=True)
                for node in ET.fromstring(xml).iter("node"):
                    if node.get("package", "").endswith("packageinstaller") and node.get("text", "").lower() in ("update", "install"):
                        bounds = [int(value) for value in re.findall(r"\d+", node.get("bounds", ""))]
                        run(*adb, "shell", "input", "tap", str((bounds[0] + bounds[2]) // 2), str((bounds[1] + bounds[3]) // 2))
                        confirmed = True
                        break
                time.sleep(1)
            else:
                raise SystemExit("Synthetic APK replacement did not complete")
            if not confirmed:
                raise SystemExit("Expected explicit Android install approval was not exercised")
            time.sleep(2)
            retained = run(*adb, "shell", "run-as", PACKAGE, "cat", "shared_prefs/synthetic-update-settings.xml", capture=True)
            if "https://synthetic.invalid" not in retained:
                raise SystemExit("Synthetic app settings were lost")
            files = run(*adb, "shell", "run-as", PACKAGE, "find", "files", "-name", "update.apk", capture=True)
            if files.strip():
                raise SystemExit("Downloaded APK was not cleaned up after replacement")
            accepted = run(*adb, "shell", "run-as", PACKAGE, "cat", "shared_prefs/accepted-release.xml", capture=True)
            if "26.10.11-RC2" not in accepted:
                raise SystemExit("Accepted RC release label was lost after APK replacement")
            focus = run(*adb, "shell", "dumpsys", "window", capture=True)
            automatic = any(PACKAGE in line for line in focus.splitlines() if "mCurrentFocus=" in line)
            print(f"PASS: APK 23 -> 24, Android approval, settings and RC label preserved, download cleaned; automatic relaunch={automatic}")
            run(*adb, "shell", "rm", "-f", "/sdcard/alreadyon-update-test.xml")
        try:
            run(*gradle, "-PandroidVersionCode=24", "-PandroidVersionName=26.10.11-alpha5",
                ":app:assembleDebug", ":tv:assembleDebug")
            for target, module in (("phone", "app"), ("tv", "tv")):
                shutil.copyfile(ROOT / f"apps/android/{module}/build/outputs/apk/debug/{module}-debug.apk",
                                assets / f"update-{target}-test.apk")
            run(*gradle, "-PandroidVersionCode=23", "-PandroidVersionName=26.10.11-alpha4",
                f"-PandroidUpdateTestAssetsDirectory={assets}", ":app:testDebugUnitTest",
                ":app:assembleDebug", ":app:assembleDebugAndroidTest", ":tv:assembleDebug", ":tv:assembleDebugAndroidTest")
            for module in ("app", "tv"):
                run(*adb, "install", "-r", str(ROOT / f"apps/android/{module}/build/outputs/apk/debug/{module}-debug.apk"))
                run(*adb, "install", "-r", str(ROOT / f"apps/android/{module}/build/outputs/apk/androidTest/debug/{module}-debug-androidTest.apk"))
                output = run(*adb, "shell", "am", "instrument", "-w", "-r", "-e", "class",
                             "io.github.alreadyonapp.alreadyon.UpdateClientTest,io.github.alreadyonapp.alreadyon.UpdateDialogTest",
                             f"{PACKAGE}.test/androidx.test.runner.AndroidJUnitRunner", capture=True)
                print(output)
                if args.screenshots:
                    args.screenshots.mkdir(parents=True, exist_ok=True)
                    with (args.screenshots / f"updater-{module}.png").open("wb") as screenshot:
                        subprocess.run([*adb, "exec-out", "run-as", PACKAGE, "cat", "cache/update-dialog.png"], check=True, stdout=screenshot)
                if "FAILURES" in output or "INSTRUMENTATION_FAILED" in output or "OK (12 tests)" not in output:
                    raise SystemExit(f"{module} updater instrumentation failed")
                verify_install_cycle()
                run(*adb, "uninstall", f"{PACKAGE}.test")
                run(*adb, "uninstall", PACKAGE)
        finally:
            # Only these new, synthetic packages are ours; no user app/session/database is reset.
            subprocess.run([*adb, "uninstall", f"{PACKAGE}.test"], check=False, stdout=subprocess.DEVNULL)
            subprocess.run([*adb, "uninstall", PACKAGE], check=False, stdout=subprocess.DEVNULL)


if __name__ == "__main__":
    main()
