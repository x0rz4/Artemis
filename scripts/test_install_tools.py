"""Offline regression tests: synthetic ELF files, no scanners or databases run."""
from contextlib import redirect_stdout
import hashlib
import io
import json
import os
from pathlib import Path
import stat
import tarfile
import tempfile
import unittest
from unittest import mock
import urllib.request
import zipfile

import install_tools as installer


def elf(machine=62, suffix=b"fixture"):
    return b"\x7fELF\x02\x01" + b"\x00" * 12 + machine.to_bytes(2, "little") + suffix


def zip_bytes(members):
    stream = io.BytesIO()
    with zipfile.ZipFile(stream, "w") as archive:
        for name, data in members:
            archive.writestr(name, data)
    return stream.getvalue()


class ToolInstallTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="artemis-tools-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / "tools"
        self.root.mkdir()
        self.tool = {"name": "httpx", "version": "1.0", "purpose": "HTTP fixture",
                     "upstream": "https://github.com/example/tool", "release": "https://github.com/example/tool/releases/tag/v1.0"}
        self.binary = elf()
        self.content = zip_bytes([("httpx", self.binary), ("LICENSE.txt", b"fixture-license")])
        self.asset = {"url": "https://example.invalid/tool.zip", "format": "zip", "member": "httpx",
                      "sha256": hashlib.sha256(self.content).hexdigest()}
        self.fetch = mock.Mock(side_effect=lambda asset, path: path.write_bytes(self.content))

    def install(self, tool=None, asset=None, fetch=None):
        with redirect_stdout(io.StringIO()):
            return installer.install([(tool or self.tool, asset or self.asset)], self.root,
                                     "linux/amd64", fetch=fetch or self.fetch)

    def seed(self, tools):
        document = {"version": "1", "sources": [{"name": "mine"}], "tools": tools, "custom": {"keep": True}}
        original = installer.json_bytes(document)
        (self.root / "manifest.json").write_bytes(original)
        return original

    def test_install_records_file_digest_and_preserves_license(self):
        manifest = self.install()
        entry = manifest["tools"][0]
        self.assertEqual(entry["sha256"], hashlib.sha256(self.binary).hexdigest())
        self.assertNotEqual(entry["sha256"], self.asset["sha256"])
        self.assertFalse(entry["enabled"])
        path = self.root / entry["path"]
        self.assertEqual(path.read_bytes(), self.binary)
        self.assertEqual((path.parent / "licenses/LICENSE.txt").read_bytes(), b"fixture-license")
        if os.name != "nt":
            self.assertTrue(os.access(path, os.X_OK))

    def test_repeat_needs_no_network_or_manifest_write_even_after_server_normalization(self):
        manifest = self.install()
        for field in ("download_sha256", "upstream", "release"):
            manifest["tools"][0].pop(field)
        (self.root / "manifest.json").write_bytes(installer.json_bytes(manifest))
        before = (self.root / "manifest.json").read_bytes()
        self.fetch.reset_mock()
        self.install()
        self.fetch.assert_not_called()
        self.assertEqual(before, (self.root / "manifest.json").read_bytes())
        self.assertFalse((self.root / ".snapshots").exists())

    def test_merge_preserves_local_tools_bindings_and_custom_metadata(self):
        unrelated = {"name": "local", "path": "local", "enabled": True}
        old = {"name": "httpx", "path": "old/httpx", "key": "my_httpx", "agents": ["my_agent"],
               "enabled": True, "deferred": True, "schema": {"custom": True}, "custom_field": "keep"}
        original = self.seed([unrelated, old])
        manifest = self.install()
        self.assertEqual(manifest["tools"][0], unrelated)
        for field in ("key", "agents", "enabled", "deferred", "schema", "custom_field"):
            self.assertEqual(manifest["tools"][1][field], old[field])
        self.assertEqual(manifest["sources"], [{"name": "mine"}])
        self.assertEqual(manifest["custom"], {"keep": True})
        snapshots = list((self.root / ".snapshots").iterdir())
        self.assertEqual(len(snapshots), 1)
        self.assertEqual((snapshots[0] / "manifest.json").read_bytes(), original)
        self.assertEqual((self.root / "manifest.json.bak").read_bytes(), original)
        self.assertEqual(json.loads((snapshots[0] / "meta.json").read_text())["items"], ["manifest.json"])

    def test_updated_version_keeps_original_files_for_snapshot_restore(self):
        old = self.install()["tools"][0]
        old_manifest = (self.root / "manifest.json").read_bytes()
        self.tool["version"] = "2.0"
        self.content = zip_bytes([("httpx", elf(suffix=b"new-version"))])
        self.asset["sha256"] = hashlib.sha256(self.content).hexdigest()
        new = self.install()["tools"][0]
        self.assertNotEqual(old["path"], new["path"])
        self.assertEqual((self.root / old["path"]).read_bytes(), self.binary)
        snapshot = next((self.root / ".snapshots").iterdir())
        self.assertEqual((snapshot / "manifest.json").read_bytes(), old_manifest)

    def test_hash_failure_leaves_existing_manifest_and_files_unchanged(self):
        original = self.seed([{"name": "local", "path": "local"}])
        (self.root / "local").write_bytes(b"keep")
        self.asset["sha256"] = "0" * 64
        with self.assertRaisesRegex(ValueError, "SHA-256"):
            self.install()
        self.assertEqual((self.root / "manifest.json").read_bytes(), original)
        self.assertEqual((self.root / "local").read_bytes(), b"keep")
        self.assertFalse((self.root / ".install-tools.lock").exists())

    def test_network_failure_keeps_original_manifest(self):
        original = self.seed([])
        with self.assertRaises(OSError):
            self.install(fetch=mock.Mock(side_effect=OSError("network unavailable")))
        self.assertEqual((self.root / "manifest.json").read_bytes(), original)

    def test_invalid_manifest_is_not_replaced(self):
        (self.root / "manifest.json").write_bytes(b"broken")
        with self.assertRaises(ValueError):
            self.install()
        self.fetch.assert_not_called()
        self.assertEqual((self.root / "manifest.json").read_bytes(), b"broken")

    def test_concurrent_script_is_rejected(self):
        with installer.install_lock(self.root.resolve()):
            with self.assertRaisesRegex(ValueError, "安装锁"):
                self.install()
        self.fetch.assert_not_called()

    def test_concurrent_manifest_edit_is_not_overwritten(self):
        original = self.seed([])
        changed = original + b"\n"
        def download_and_edit(asset, path):
            path.write_bytes(self.content)
            (self.root / "manifest.json").write_bytes(changed)
        with self.assertRaisesRegex(ValueError, "已变化"):
            self.install(fetch=download_and_edit)
        self.assertEqual((self.root / "manifest.json").read_bytes(), changed)

    def test_atomic_manifest_failure_keeps_original(self):
        original = self.seed([])
        real_replace = os.replace
        def fail_manifest(src, dst):
            if Path(dst).name == "manifest.json":
                raise OSError("disk failure")
            return real_replace(src, dst)
        with mock.patch.object(installer.os, "replace", side_effect=fail_manifest):
            with self.assertRaises(OSError):
                self.install()
        self.assertEqual((self.root / "manifest.json").read_bytes(), original)

    def test_bad_paths_and_duplicate_members_are_rejected(self):
        for members in ([('../escape', b'x')], [('/absolute', b'x')], [('a\\b', b'x')],
                        [('httpx', self.binary), ('HTTPX', self.binary)]):
            with self.subTest(members=members[0][0]):
                self.content = zip_bytes(members)
                self.asset["sha256"] = hashlib.sha256(self.content).hexdigest()
                with self.assertRaises(ValueError):
                    self.install()
        self.assertFalse((self.root / "manifest.json").exists())

    def test_zip_symlink_is_rejected(self):
        stream = io.BytesIO()
        info = zipfile.ZipInfo("httpx")
        info.create_system = 3
        info.external_attr = (stat.S_IFLNK | 0o777) << 16
        with zipfile.ZipFile(stream, 'w') as z:
            z.writestr(info, "../../outside")
        self.content = stream.getvalue()
        self.asset["sha256"] = hashlib.sha256(self.content).hexdigest()
        with self.assertRaisesRegex(ValueError, "链接"):
            self.install()

    def test_tar_and_raw_binary_install(self):
        for fmt in ("tar.gz", "binary"):
            with self.subTest(format=fmt):
                if fmt == "tar.gz":
                    stream = io.BytesIO()
                    with tarfile.open(fileobj=stream, mode="w:gz") as tar:
                        info = tarfile.TarInfo("httpx")
                        info.size = len(self.binary)
                        tar.addfile(info, io.BytesIO(self.binary))
                    self.content = stream.getvalue()
                else:
                    self.content = self.binary
                self.asset.update(format=fmt, sha256=hashlib.sha256(self.content).hexdigest())
                self.assertEqual(self.install()["tools"][0]["sha256"], hashlib.sha256(self.binary).hexdigest())

    def test_tar_hardlink_is_rejected(self):
        stream = io.BytesIO()
        with tarfile.open(fileobj=stream, mode="w:gz") as tar:
            info = tarfile.TarInfo("httpx")
            info.type, info.linkname = tarfile.LNKTYPE, "../../outside"
            tar.addfile(info)
        self.content = stream.getvalue()
        self.asset.update(format="tar.gz", sha256=hashlib.sha256(self.content).hexdigest())
        with self.assertRaisesRegex(ValueError, "链接"):
            self.install()

    def test_key_collision_preserves_existing_manifest(self):
        before = self.seed([{"name": "local", "key": "httpx", "path": "local"}])
        with self.assertRaisesRegex(ValueError, "key 冲突"):
            self.install()
        self.assertEqual((self.root / "manifest.json").read_bytes(), before)

    def test_download_size_limit_and_https_redirect(self):
        opener = mock.Mock()
        opener.open.return_value = io.BytesIO(b"over limit")
        with mock.patch.object(installer.urllib.request, "build_opener", return_value=opener), \
                mock.patch.object(installer, "MAX_DOWNLOAD", 1):
            with self.assertRaisesRegex(ValueError, "下载超过"):
                installer.download(self.asset, self.root / "download")
        handler = installer.HTTPSRedirect()
        req = urllib.request.Request("https://example.invalid/asset")
        with self.assertRaisesRegex(ValueError, "HTTPS"):
            handler.redirect_request(req, None, 302, "Found", {}, "http://example.invalid/asset")

    def test_wrong_architecture_and_missing_member(self):
        for members in ([('httpx', elf(machine=183))], [('wrong', self.binary)]):
            self.content = zip_bytes(members)
            self.asset["sha256"] = hashlib.sha256(self.content).hexdigest()
            with self.assertRaises(ValueError):
                self.install()

    def test_snapshot_retention(self):
        for number in range(7):
            installer.snapshot_manifest(self.root.resolve(), str(number).encode())
        self.assertEqual(len(list((self.root / ".snapshots").iterdir())), 5)

    def test_download_limit_and_hash_lock_are_enforced(self):
        with mock.patch.object(installer, "MAX_EXTRACT", 1):
            with self.assertRaisesRegex(ValueError, "大小"):
                self.install()
        lock = installer.ROOT / "packaging/tools-lock.json"
        plan = installer.load_plan(lock, "baseline", "linux/amd64")
        self.assertEqual(len(plan), 8)
        self.assertTrue(all(installer.SHA256.fullmatch(a['sha256']) for _, a in plan))
        with self.assertRaisesRegex(ValueError, "kscan"):
            installer.load_plan(lock, "baseline", "linux/arm64")
        self.assertEqual(len(installer.load_plan(lock, "httpx,naabu,subfinder,katana,ffuf,nuclei,fscan", "linux/arm64")), 7)

    def test_download_lock_validation(self):
        lock = self.root / "lock.json"
        for field, value in (("sha256", ""), ("url", "http://example.invalid/x"), ("member", "../x")):
            asset = dict(self.asset, **{field: value})
            lock.write_bytes(installer.json_bytes({"version": 1, "tools": [dict(self.tool, assets={"linux/amd64": asset})]}))
            with self.subTest(field=field), self.assertRaises(ValueError):
                installer.load_plan(lock, "httpx", "linux/amd64")

    def test_platform_aliases(self):
        self.assertEqual(installer.target_platform("linux/x86_64"), "linux/amd64")
        self.assertEqual(installer.target_platform("linux/aarch64"), "linux/arm64")
        with self.assertRaises(ValueError):
            installer.target_platform("darwin/arm64")


if __name__ == "__main__":
    unittest.main()
