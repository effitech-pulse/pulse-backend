"""Регрессии синхронизации на временных копиях, без доступа к runtime/БД."""
from __future__ import annotations

from contextlib import redirect_stderr, redirect_stdout
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
import zipfile

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[1]


class SourceSyncTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="pulse-doc-sync-")
        self.addCleanup(self.temporary.cleanup)
        self.base = Path(self.temporary.name)
        self.workspace = self.base / "workspace"
        self.package = self.base / "package"
        shutil.copytree(ROOT, self.package, ignore=shutil.ignore_patterns("__pycache__"))
        registry = json.loads((ROOT / "inventory/snapshot-sources.json").read_text())
        original_workspace = ROOT.parents[2]
        for repo in ("pulse-model", "pulse-data", "pulse-ml-service", "pulse-contracts",
                     "pulse-backend", "pulse-frontend", "pulse-landing"):
            (self.workspace / repo).mkdir(parents=True, exist_ok=True)
        for entry in registry["entries"]:
            target = self.workspace / entry["source"]
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(original_workspace / entry["source"], target)
        spec = importlib.util.spec_from_file_location("fixture_sync", self.package / "tools/sync_sources.py")
        self.module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.module)
        self.assertEqual(self.invoke("--sync"), 0)
        self.assertEqual(self.invoke("--check"), 0)

    def invoke(self, mode: str) -> int:
        # Git-идентичность fixture подставляется отдельно от проверяемого source/copy кода.
        # Репозитории, Git index и настройки пользователя не изменяются.
        def git_result(command: list[str], **kwargs: object) -> str:
            if command[1:3] == ["rev-parse", "HEAD"]:
                return "0" * 40 + "\n"
            if command[1:3] == ["branch", "--show-current"]:
                return "fixture\n"
            return ""

        with patch.object(sys, "argv", ["sync_sources.py", "--workspace", str(self.workspace), mode]), \
                patch.object(self.module.subprocess, "check_output", side_effect=git_result), \
                redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
            return self.module.main()

    def test_changed_source_is_detected_and_sync_restores_consistency(self) -> None:
        source = self.workspace / "pulse-contracts/buf.yaml"
        source.write_bytes(source.read_bytes() + b"\n# fixture change\n")
        self.assertEqual(self.invoke("--check"), 1)
        self.assertEqual(self.invoke("--sync"), 0)
        self.assertEqual(self.invoke("--check"), 0)

    def test_new_public_source_is_detected_and_sync_updates_inventory(self) -> None:
        source = self.workspace / "pulse-backend/docs/fixture-new-source.md"
        source.parent.mkdir(parents=True, exist_ok=True)
        source.write_text("# Fixture source\n")
        self.assertEqual(self.invoke("--check"), 1)
        self.assertEqual(self.invoke("--sync"), 0)
        self.assertEqual(self.invoke("--check"), 0)
        self.assertIn("fixture-new-source.md", (self.package / "inventory/source-files.csv").read_text())

    def test_changed_snapshot_is_detected_and_sync_restores_source(self) -> None:
        snapshot = self.package / "contracts/ml.proto"
        snapshot.write_bytes(snapshot.read_bytes() + b"\n// fixture corruption\n")
        self.assertEqual(self.invoke("--check"), 1)
        self.assertEqual(self.invoke("--sync"), 0)
        self.assertEqual(self.invoke("--check"), 0)
        self.assertEqual(snapshot.read_bytes(), (self.workspace / "pulse-contracts/proto/pulse/ml/v1/ml.proto").read_bytes())

    def test_publication_date_is_explicit_and_check_uses_registry(self) -> None:
        registry_path = self.package / "inventory/snapshot-sources.json"
        registry = json.loads(registry_path.read_text())
        registry["snapshot_date"] = "2030-01-02"
        registry_path.write_text(json.dumps(registry))
        self.assertEqual(self.invoke("--check"), 1)
        self.assertEqual(self.invoke("--sync"), 0)
        self.assertEqual(self.invoke("--check"), 0)
        self.assertIn("02.01.2030", (self.package / "references/verification-matrix.md").read_text().splitlines()[0])

    def package_digests(self) -> dict[str, str]:
        # Registry редактируется самим тестом; остальные файлы не должны меняться при отказе.
        return {p.relative_to(self.package).as_posix(): hashlib.sha256(p.read_bytes()).hexdigest()
                for p in self.package.rglob("*") if p.is_file() and not p.is_symlink()
                and p != self.package / "inventory/snapshot-sources.json"}

    def test_secret_source_symlink_is_rejected_before_any_partial_write(self) -> None:
        secret = self.workspace / "workspace-data/.env"
        secret.parent.mkdir()
        secret.write_text("SYNTHETIC_SECRET_SENTINEL_FOR_TEST_ONLY")
        source = self.workspace / "pulse-contracts/buf.yaml"
        source.unlink()
        source.symlink_to(secret)
        before = self.package_digests()
        with self.assertRaisesRegex(ValueError, "Symlink registry path rejected"):
            self.invoke("--sync")
        self.assertEqual(before, self.package_digests())
        self.assertNotIn("SYNTHETIC_SECRET_SENTINEL", (self.package / "contracts/buf.yaml").read_text())

    def test_known_secret_registry_path_is_rejected_without_partial_write(self) -> None:
        secret = self.workspace / "pulse-contracts/.env"
        secret.write_text("SYNTHETIC_SECRET_SENTINEL_FOR_TEST_ONLY")
        registry_path = self.package / "inventory/snapshot-sources.json"
        registry = json.loads(registry_path.read_text())
        registry["entries"][-1]["source"] = "pulse-contracts/.env"
        registry_path.write_text(json.dumps(registry))
        before = self.package_digests()
        with self.assertRaises(SystemExit):
            self.invoke("--sync")
        self.assertEqual(before, self.package_digests())

    def test_source_parent_symlink_is_rejected_without_partial_write(self) -> None:
        private = self.workspace / "workspace-data"
        private.mkdir()
        (private / "buf.yaml").write_text("SYNTHETIC_SECRET_SENTINEL_FOR_TEST_ONLY")
        (self.workspace / "pulse-contracts/alias-dir").symlink_to(private, target_is_directory=True)
        registry_path = self.package / "inventory/snapshot-sources.json"
        registry = json.loads(registry_path.read_text())
        registry["entries"].append({"source": "pulse-contracts/alias-dir/buf.yaml", "snapshot": "references/fixture-buf.yaml",
                                    "mode": "copy", "owner": "fixture"})
        registry_path.write_text(json.dumps(registry))
        before = self.package_digests()
        with self.assertRaisesRegex(ValueError, "Symlink registry path rejected"):
            self.invoke("--sync")
        self.assertEqual(before, self.package_digests())

    def test_target_parent_symlink_is_rejected_without_partial_write(self) -> None:
        (self.package / "alias").symlink_to(self.package / "references", target_is_directory=True)
        registry_path = self.package / "inventory/snapshot-sources.json"
        registry = json.loads(registry_path.read_text())
        registry["entries"].append({"source": "pulse-contracts/buf.yaml", "snapshot": "alias/fixture-buf.yaml",
                                    "mode": "copy", "owner": "fixture"})
        registry_path.write_text(json.dumps(registry))
        before = self.package_digests()
        with self.assertRaisesRegex(ValueError, "Symlink registry path rejected"):
            self.invoke("--sync")
        self.assertEqual(before, self.package_digests())
        self.assertFalse((self.package / "references/fixture-buf.yaml").exists())

    def test_extracted_zip_validates_without_workspace(self) -> None:
        self.build()
        detached = self.base / "detached"
        with zipfile.ZipFile(self.package / "pulse-project-documentation.zip") as archive:
            archive.extractall(detached)
        # Удалён fixture workspace: autonomous validator не может к нему обратиться.
        shutil.rmtree(self.workspace)
        extracted = detached / "pulse-project-documentation"
        result = subprocess.run([sys.executable, str(extracted / "tools/validate.py")],
                                cwd=extracted, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("mapped snapshots", result.stdout)

    def build(self) -> None:
        result = subprocess.run([sys.executable, str(self.package / "tools/build.py")], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def validate(self) -> subprocess.CompletedProcess:
        return subprocess.run([sys.executable, str(self.package / "tools/validate.py")],
                              capture_output=True, text=True)

    def test_missing_checksums_are_rejected(self) -> None:
        self.build()
        (self.package / "checksums.sha256").unlink()
        result = self.validate()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Missing checksums.sha256", result.stdout)

    def test_stale_checksums_are_rejected(self) -> None:
        self.build()
        policy = self.package / "SOURCE_POLICY.md"
        policy.write_bytes(policy.read_bytes() + b"\nfixture change\n")
        result = self.validate()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Checksum mismatch: SOURCE_POLICY.md", result.stdout)

    def test_zip_bytes_mismatch_is_rejected_even_with_same_names(self) -> None:
        self.build()
        archive = self.package / "pulse-project-documentation.zip"
        with zipfile.ZipFile(archive) as original:
            entries = {name: original.read(name) for name in original.namelist()}
        entries["pulse-project-documentation/README.md"] += b"\nfixture stale archive\n"
        with zipfile.ZipFile(archive, "w") as changed:
            for name, data in entries.items():
                changed.writestr(name, data)
        result = self.validate()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("ZIP bytes differ from folder: README.md", result.stdout)

    def test_zip_file_set_mismatch_is_rejected(self) -> None:
        self.build()
        with zipfile.ZipFile(self.package / "pulse-project-documentation.zip", "a") as archive:
            archive.writestr("pulse-project-documentation/fixture-extra.txt", "extra")
        result = self.validate()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("ZIP content differs from folder", result.stdout)

    def test_zip_path_traversal_is_rejected_without_extraction(self) -> None:
        self.build()
        with zipfile.ZipFile(self.package / "pulse-project-documentation.zip", "a") as archive:
            archive.writestr("../fixture-outside.txt", "outside")
        result = self.validate()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Forbidden ZIP path: ../fixture-outside.txt", result.stdout)
        self.assertFalse((self.base / "fixture-outside.txt").exists())


if __name__ == "__main__":
    unittest.main()
