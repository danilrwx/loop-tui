"""Offline installer integration checks; run after make build on Linux."""

import hashlib
import io
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest


ROOT = Path(__file__).resolve().parent.parent


class InstallerTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.home = self.base / "home"
        self.home.mkdir()
        self.mock = self.base / "mock"
        self.mock.mkdir()
        self.release = self.base / "release"
        self.release.mkdir()
        self.log = self.base / "systemctl.log"
        self.env = {
            **os.environ,
            "HOME": str(self.home),
            "XDG_CONFIG_HOME": str(self.home / "config"),
            "PATH": str(self.mock) + os.pathsep + os.environ["PATH"],
            "TEST_RELEASE": str(self.release),
            "TEST_REAL_BINARY": str(ROOT / "loop"),
            "TEST_SYSTEMD_LOG": str(self.log),
            "TEST_SYSTEMD_AVAILABLE": "yes",
        }
        self.env.pop("LOOP_INSTALL_DIR", None)
        self.write_executable(self.mock / "uname", '#!/bin/sh\ncase "$1" in -s) echo Linux;; -m) echo x86_64;; esac\n')
        self.write_executable(self.mock / "curl", '#!/bin/sh\ncp "$TEST_RELEASE/${2##*/}" "$4"\n')
        self.write_executable(self.mock / "systemctl", '''#!/bin/sh
printf '%s\n' "$*" >> "$TEST_SYSTEMD_LOG"
case "$2" in
 show-environment) test "$TEST_SYSTEMD_AVAILABLE" = yes ;;
 is-active) exit 3 ;;
esac
''')
        self.binary = b'''#!/bin/sh
case "$1" in
 --help) echo '-install-notification-service string' ;;
 --install-notification-service) exec "$TEST_REAL_BINARY" "$@" ;;
esac
'''
        self.archive(self.binary)

    @staticmethod
    def write_executable(path, content):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)
        path.chmod(0o755)

    def archive(self, binary, legacy=False):
        archive = self.release / "loop-tui_linux_amd64.tar.gz"
        with tarfile.open(archive, "w:gz") as tar:
            for name in (["loop-tui"] if legacy else ["loop", "loopd"]):
                entry = tarfile.TarInfo(name)
                entry.size, entry.mode = len(binary), 0o755
                tar.addfile(entry, io.BytesIO(binary))
        checksum = hashlib.sha256(archive.read_bytes()).hexdigest()
        (self.release / "SHA256SUMS").write_text(f"{checksum}  {archive.name}\n")

    def install(self, success=True):
        result = subprocess.run(["sh", str(ROOT / "scripts/install.sh")], env=self.env, text=True, capture_output=True)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        return result

    @property
    def unit(self):
        return Path(self.env["XDG_CONFIG_HOME"]) / "systemd/user/loop-tui-notifications.service"

    def test_install_and_repeat_with_custom_path(self):
        target = self.home / 'custom space/100%/$user/"bin'
        self.env["LOOP_INSTALL_DIR"] = str(target)
        for _ in range(2):
            self.install()
            self.assertEqual((target / "loop").read_bytes(), self.binary)
            self.assertIn('100%%/$$user/\\"bin/loopd"', self.unit.read_text())
        self.assertEqual(self.log.read_text().count("--user enable --now loop-tui-notifications.service"), 2)

    def test_existing_sso_launcher_is_preserved(self):
        launcher = self.home / ".local/bin/loop"
        companion = self.home / ".local/libexec/loop"
        self.write_executable(launcher, "#!/usr/bin/env python3\n# existing SSO launcher\n")
        self.write_executable(companion, "old binary")
        before = launcher.read_bytes()
        self.install()
        self.assertEqual(launcher.read_bytes(), before)
        self.assertEqual(companion.read_bytes(), self.binary)
        self.assertIn(f'ExecStart="{companion.parent / "loopd"}"', self.unit.read_text())

    def test_no_user_systemd_still_installs_unit(self):
        self.env["TEST_SYSTEMD_AVAILABLE"] = "no"
        result = self.install()
        self.assertTrue(self.unit.exists())
        self.assertIn("after logging in", result.stdout)
        self.assertNotIn("enable", self.log.read_text())

    def test_legacy_release_does_not_touch_service(self):
        self.archive(b"#!/bin/sh\necho legacy\n", legacy=True)
        result = self.install()
        self.assertIn("service installation skipped", result.stdout)
        self.assertFalse(self.unit.exists())
        self.assertFalse(self.log.exists())

    def test_daemon_has_no_terminal_dependencies(self):
        deps = subprocess.check_output(["go", "list", "-deps", "./cmd/loopd"], cwd=ROOT, text=True)
        for package in ("charmbracelet", "telega/tui", "goldmark", "kyokomi/emoji"):
            self.assertNotIn(package, deps)

    def test_bad_checksum_leaves_installation_untouched(self):
        (self.release / "SHA256SUMS").write_text("bad  loop-tui_linux_amd64.tar.gz\n")
        self.install(success=False)
        self.assertFalse((self.home / ".local/bin/loop").exists())
        self.assertFalse(self.unit.exists())


if __name__ == "__main__":
    unittest.main()
