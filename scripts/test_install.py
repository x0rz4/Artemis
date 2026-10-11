"""Installer failure-path and credential-preservation tests; no real Docker/DB required."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
BASH = shutil.which("bash") or "C:/Program Files/Git/bin/bash.exe"


class InstallTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="artemis-install-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / "bin").mkdir()
        (self.root / "scripts").mkdir()
        for name in ("install.sh", ".env.example", "go.mod"):
            shutil.copyfile(ROOT / name, self.root / name)
        shutil.copyfile(ROOT / "scripts/install_config.py", self.root / "scripts/install_config.py")
        self.env = {k: v for k, v in os.environ.items() if not k.startswith(("ARTEX_", "POSTGRES_", "MOCK_"))}
        self.env["PATH"] = str(self.root / "bin") + os.pathsep + self.env["PATH"]
        self.env["MOCK_LOG"] = (self.root / "calls.log").as_posix()
        mock_bin = (self.root / "bin").as_posix()
        if os.name == "nt":
            mock_bin = "/" + mock_bin[0].lower() + mock_bin[2:]
        self.env["MOCK_BIN"] = mock_bin
        self.env["PYTHONUTF8"] = "1"
        self.mock("docker", '''printf '%s\\n' "$*" >> "$MOCK_LOG"
case "$*" in
  info) exit "${MOCK_DAEMON_FAIL:-0}" ;;
  'compose up --help') echo --wait-timeout ;;
  *'config --quiet') exit "${MOCK_CONFIG_FAIL:-0}" ;;
  *'pull artemis') exit "${MOCK_PULL_FAIL:-0}" ;;
  *'build artemis') exit "${MOCK_BUILD_FAIL:-0}" ;;
  *'up -d'*) exit "${MOCK_UP_FAIL:-0}" ;;
  *'port artemis 8787') echo 127.0.0.1:8787 ;;
esac
''')

    def mock(self, name, body):
        path = self.root / "bin" / name
        path.write_text("#!/usr/bin/env bash\n" + body, encoding="utf-8", newline="\n")
        path.chmod(0o755)

    def run_install(self, *args):
        return subprocess.run([BASH, "--noprofile", "--norc", "-c",
                              'export PATH="$MOCK_BIN:$PATH"; exec bash "$@"',
                              "installer-test", str(self.root / "install.sh"), *args], cwd=self.root,
                              env=self.env, capture_output=True, text=True, encoding="utf-8", timeout=30)

    def calls(self):
        path = self.root / "calls.log"
        return path.read_text() if path.exists() else ""

    def test_help_and_invalid_arguments_have_no_side_effects(self):
        self.assertEqual(self.run_install("--help").returncode, 0)
        self.assertNotEqual(self.run_install("--mode", "typo").returncode, 0)
        self.assertNotEqual(self.run_install("--mode").returncode, 0)
        self.assertEqual(self.calls(), "")
        self.assertFalse((self.root / ".env").exists())

    def test_first_install_random_password_and_repeat_preserves_env(self):
        result = self.run_install("--yes", "--no-start")
        self.assertEqual(result.returncode, 0, result.stderr)
        before = (self.root / ".env").read_bytes()
        password = next(line.split("=", 1)[1] for line in before.decode().splitlines()
                        if line.startswith("POSTGRES_PASSWORD="))
        self.assertRegex(password, r"^[a-f0-9]{48}$")
        self.assertNotIn(password, result.stdout + result.stderr)
        self.assertNotIn("up -d", self.calls())
        result = self.run_install("--yes", "--no-start")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(before, (self.root / ".env").read_bytes())

    def test_existing_env_is_not_executed_or_overwritten(self):
        content = 'POSTGRES_PASSWORD=existing\nSIDE_EFFECT=$(touch executed)\n'
        (self.root / ".env").write_text(content)
        result = self.run_install("--yes", "--no-start")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.root / ".env").read_text(), content)
        self.assertFalse((self.root / "executed").exists())

    def test_failed_pull_builds_before_start(self):
        self.env["MOCK_PULL_FAIL"] = "1"
        result = self.run_install("--yes")
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.calls()
        self.assertLess(calls.index("build artemis"), calls.index("up -d"))
        self.assertIn("--wait --wait-timeout", calls)

    def test_explicit_build_never_pulls_application(self):
        result = self.run_install("--yes", "--build", "--no-start")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("pull artemis", self.calls())
        self.assertIn("build artemis", self.calls())

    def test_daemon_failure_does_not_write_config(self):
        self.env["MOCK_DAEMON_FAIL"] = "1"
        self.assertNotEqual(self.run_install("--yes").returncode, 0)
        self.assertFalse((self.root / ".env").exists())

    def test_config_and_build_failures_never_start_services(self):
        for failure in ("MOCK_CONFIG_FAIL", "MOCK_BUILD_FAIL"):
            with self.subTest(failure=failure):
                self.env[failure] = "1"
                self.assertNotEqual(self.run_install("--yes", "--build").returncode, 0)
                self.assertNotIn("up -d", self.calls())
                del self.env[failure]

    def test_unhealthy_service_is_not_reported_as_success(self):
        self.env["MOCK_UP_FAIL"] = "1"
        result = self.run_install("--yes")
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("服务已通过健康检查", result.stdout)

    def local_mocks(self):
        self.mock("uname", "echo Linux\n")
        self.mock("go", '''case "$*" in
  'env GOVERSION') echo go1.26.3 ;;
  'env GOOS') echo linux ;;
  'env GOARCH') echo amd64 ;;
esac
''')
        for command in ("node", "npm", "rsync"):
            self.mock(command, "exit 0\n")
        (self.root / "build.sh").write_text('#!/bin/bash\nprintf "build %s\\n" "$*" >> "$MOCK_LOG"\n')

    def test_local_config_escapes_password_and_is_preserved(self):
        self.local_mocks()
        password = 'space " quote \\ dollar $ and newline\n密码'
        self.env["POSTGRES_PASSWORD"] = password
        result = self.run_install("--mode", "local", "--yes", "--no-start")
        self.assertEqual(result.returncode, 0, result.stderr)
        config = self.root / "config.json"
        self.assertEqual(json.loads(config.read_text(encoding="utf-8"))["database"]["password"], password)
        before = config.read_bytes()
        self.env["POSTGRES_PASSWORD"] = "different"
        result = self.run_install("--mode", "local", "--yes", "--no-start")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(config.read_bytes(), before)
        self.assertIn("build --target linux/amd64", self.calls())

    def test_missing_explicit_config_is_not_silently_replaced(self):
        self.local_mocks()
        self.env["ARTEX_CONFIG"] = "missing.json"
        self.assertNotEqual(self.run_install("--mode", "local", "--yes", "--no-start").returncode, 0)
        self.assertFalse((self.root / "config.json").exists())

    def test_compose_config_uses_effective_credentials_and_port(self):
        config = {"services": {"postgres": {"environment": {
            "POSTGRES_USER": "custom", "POSTGRES_PASSWORD": "p@ss:'\\$", "POSTGRES_DB": "database"},
            "ports": [{"target": 5432, "published": "15432"}]}}}
        result = subprocess.run([sys.executable, str(ROOT / "scripts/install_config.py"), "--compose"],
            cwd=self.root, input=json.dumps(config), capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        database = json.loads((self.root / "config.json").read_text())["database"]
        self.assertEqual(database["port"], 15432)
        self.assertEqual(database["password"], config["services"]["postgres"]["environment"]["POSTGRES_PASSWORD"])
        # Direct invocation must never replace an existing config.
        result = subprocess.run([sys.executable, str(ROOT / "scripts/install_config.py"), "--compose"],
            cwd=self.root, input=json.dumps(config), capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()
