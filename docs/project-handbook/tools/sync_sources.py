"""Сверяет снимки с workspace; автономная проверка пакета остаётся validate.py."""
from __future__ import annotations

import argparse
import csv
from datetime import date
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]
REPOSITORIES = (
    "pulse-model", "pulse-data", "pulse-ml-service", "pulse-contracts",
    "pulse-backend", "pulse-frontend", "pulse-landing",
)
SUFFIXES = {
    ".py", ".go", ".proto", ".md", ".toml", ".json", ".yaml", ".yml",
    ".txt", ".html", ".css", ".ts", ".tsx", ".js", ".mjs", ".sql", ".mod", ".sum",
}
SKIP_DIRS = {
    ".git", ".venv", "venv", "node_modules", "data", "dist", "__pycache__",
    ".pytest_cache", ".mypy_cache", ".ruff_cache", "catboost_info", ".ci-model",
    ".ci-contracts", "bin", "build", "archive", "reference", ".idea",
}
SKIP_NAMES = {".env", ".npmrc", ".netrc", "id_rsa", "id_ed25519"}
PRIVATE_PARTS = {"data", "workspace-data", ".venv", "venv", ".git", ".aws", ".ssh", "node_modules", "archive", "reference"}


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def local_path(parent: Path, relative: str) -> Path:
    """Принимает только относительный путь внутри явно заданного корня."""
    value = Path(relative)
    if value.is_absolute() or ".." in value.parts:
        raise ValueError(f"Unsafe registry path: {relative}")
    candidate = parent
    for part in value.parts:
        candidate = candidate / part
        if candidate.is_symlink():
            raise ValueError(f"Symlink registry path rejected: {relative}")
    result = (parent / value).resolve()
    result.relative_to(parent.resolve())
    return result


def source_files(workspace: Path) -> list[dict]:
    """Индексирует публичные исходники; данные, секреты и сам пакет исключены."""
    rows = []
    for repo in REPOSITORIES:
        base = workspace / repo
        for directory, dirs, names in os.walk(base, followlinks=False):
            folder = Path(directory)
            dirs[:] = sorted(
                name for name in dirs
                if name not in SKIP_DIRS and name not in PRIVATE_PARTS and not name.endswith(".egg-info")
                and not (folder / name).is_symlink()
                and not (repo == "pulse-backend" and folder / name == base / "docs/project-handbook")
            )
            # В backend build/package содержит публичный Dockerfile.
            if folder == base and repo == "pulse-backend" and (base / "build/package").is_dir():
                dirs.append("build")
            if folder == base / "build" and repo == "pulse-backend":
                dirs[:] = ["package"]
            for name in sorted(names):
                file = folder / name
                if file.is_symlink() or name in SKIP_NAMES or name.startswith(".env"):
                    continue
                if file.suffix not in SUFFIXES and name not in {"Dockerfile", "Makefile"}:
                    continue
                data = file.read_bytes()
                rows.append({"repository": repo, "path": file.relative_to(base).as_posix(),
                             "sha256": digest(data), "bytes": len(data)})
    return sorted(rows, key=lambda row: (row["repository"], row["path"]))


def rewrite_markdown(data: bytes, source: Path, target: Path, workspace: Path,
                     destinations: dict[Path, Path], snapshot_date: str) -> bytes:
    """Локализует ссылки копии; непоставляемые исходники оставляет текстом."""
    def link(match: re.Match) -> str:
        label, url = match.group(1), match.group(2)
        parts = urlsplit(url)
        if parts.scheme or parts.netloc or not parts.path:
            return match.group(0)
        original = (source.parent / unquote(parts.path)).resolve()
        copied = destinations.get(original)
        if copied is None:
            try:
                relative_package = original.relative_to(workspace / "pulse-backend/docs/project-handbook")
                copied = ROOT / relative_package
            except ValueError:
                pass
        if copied is not None:
            relative = Path(os.path.relpath(copied, target.parent)).as_posix()
            return f"[{label}]({relative}" + (f"#{parts.fragment}" if parts.fragment else "") + ")"
        try:
            path = original.relative_to(workspace).as_posix()
        except ValueError:
            raise ValueError(f"Outside workspace link in {source.name}: {url}") from None
        return f"{label} (исходник `{path}`; в пакет не включён)"

    # Ссылки в code fences являются примерами и не преобразуются.
    chunks = re.split(r"(```.*?```)", data.decode("utf-8"), flags=re.S)
    text = "".join(chunk if i % 2 else re.sub(r"\[([^\]]*)\]\(([^)]+)\)", link, chunk)
                   for i, chunk in enumerate(chunks))
    origin = source.relative_to(workspace).as_posix()
    label_date = date.fromisoformat(snapshot_date).strftime("%d.%m.%Y")
    return (f"> Снимок документа {label_date}. Источник: `{origin}`; связь и владелец — "
            "`inventory/snapshot-sources.json`. Ссылки локализованы для автономного пакета.\n\n" + text).encode()


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workspace", type=Path, required=True,
                        help="Корень семи checkout repository-split; данные/секреты не читаются")
    modes = parser.add_mutually_exclusive_group(required=True)
    modes.add_argument("--check", action="store_true", help="Только сверка, без записи")
    modes.add_argument("--sync", action="store_true", help="Обновить копии и inventory; затем build/validate отдельно")
    args = parser.parse_args()
    workspace = args.workspace.resolve()
    for repo in REPOSITORIES:
        if not (workspace / repo).is_dir():
            parser.error(f"Missing checkout: {repo}")
    registry_path = ROOT / "inventory/snapshot-sources.json"
    registry = json.loads(registry_path.read_text())
    snapshot_date = registry["snapshot_date"]
    date.fromisoformat(snapshot_date)
    entries = registry["entries"]
    targets = set()
    for entry in entries:
        source_parts = Path(entry["source"]).parts
        if not source_parts or source_parts[0] not in REPOSITORIES or any(
            part in SKIP_NAMES or part.startswith(".env") or part in PRIVATE_PARTS
            for part in source_parts
        ):
            parser.error("Non-public registry source: " + entry["source"])
        if any(part in SKIP_NAMES or part.startswith(".env") or part in PRIVATE_PARTS
               for part in Path(entry["snapshot"]).parts):
            parser.error("Non-public registry target: " + entry["snapshot"])
        if entry["mode"] not in {"copy", "markdown"} or not entry.get("owner"):
            parser.error("Registry mode/owner required: " + entry["snapshot"])
        target = local_path(ROOT, entry["snapshot"])
        if target in targets:
            parser.error("Duplicate snapshot target: " + entry["snapshot"])
        targets.add(target)
        source = local_path(workspace, entry["source"])
        resolved_parts = source.relative_to(workspace).parts
        if resolved_parts[0] not in REPOSITORIES or any(
            part in SKIP_NAMES or part.startswith(".env") or part in PRIVATE_PARTS
            for part in resolved_parts
        ):
            parser.error("Non-public resolved registry source: " + entry["source"])
        if not source.is_file():
            parser.error("Missing public source: " + entry["source"])
    destinations = {local_path(workspace, e["source"]): local_path(ROOT, e["snapshot"])
                    for e in entries}
    errors = []
    updated = []
    for entry in entries:
        source = local_path(workspace, entry["source"])
        target = local_path(ROOT, entry["snapshot"])
        raw = source.read_bytes()
        data = rewrite_markdown(raw, source, target, workspace, destinations, snapshot_date) if entry["mode"] == "markdown" else raw
        actual = target.read_bytes() if target.exists() else None
        if args.sync:
            target.parent.mkdir(parents=True, exist_ok=True)
            if actual != data:
                target.write_bytes(data)
                updated.append(entry["snapshot"])
            entry["source_sha256"] = digest(raw)
            entry["snapshot_sha256"] = digest(data)
        elif actual != data or entry.get("source_sha256") != digest(raw) or entry.get("snapshot_sha256") != digest(data):
            errors.append("Source/snapshot drift: " + entry["snapshot"])
    rows = source_files(workspace)
    csv_path = ROOT / "inventory/source-files.csv"
    if args.sync:
        buffer = io.StringIO(newline="")
        writer = csv.DictWriter(buffer, fieldnames=["repository", "path", "sha256", "bytes"])
        writer.writeheader()
        writer.writerows(rows)
        csv_path.write_text(buffer.getvalue())
        registry_path.write_text(json.dumps(registry, ensure_ascii=False, indent=2) + "\n")
        manifest_path = ROOT / "inventory/source-manifest.json"
        manifest = json.loads(manifest_path.read_text())
        for item in manifest["repositories"]:
            repo = workspace / item["name"]
            item["head"] = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repo, text=True).strip()
            item["branch"] = subprocess.check_output(["git", "branch", "--show-current"], cwd=repo, text=True).strip()
            item["dirty_entries"] = len(subprocess.check_output(["git", "status", "--porcelain"], cwd=repo, text=True).splitlines())
        manifest["source_file_count"] = len(rows)
        manifest["snapshot_registry"] = "snapshot-sources.json"
        manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
        metadata_path = ROOT / "inventory/documentation-metadata.json"
        metadata = json.loads(metadata_path.read_text())
        metadata["source_records"] = len(rows)
        metadata["sources_check"] = "python3 tools/sync_sources.py --workspace <repository-split> --check"
        metadata_path.write_text(json.dumps(metadata, ensure_ascii=False, indent=2) + "\n")
        chapter = ROOT / "chapters/18-code-inventory.md"
        text = chapter.read_text()
        for item in manifest["repositories"]:
            pattern = r"\| " + re.escape(item["name"]) + r" \| `[^`]+` \| \d+ \|"
            text = re.sub(pattern, f'| {item["name"]} | `{item["head"][:12]}` | {item["dirty_entries"]} |', text)
        chapter.write_text(text)
    else:
        with csv_path.open(newline="") as handle:
            stored = list(csv.DictReader(handle))
        comparable = [{**row, "bytes": str(row["bytes"])} for row in rows]
        if stored != comparable:
            errors.append("Source inventory drift (changed, new or missing public source)")
    if errors:
        print("\n".join(errors))
        return 1
    print(f"OK: {len(entries)} source/snapshot mappings; {len(rows)} public source files; "
          + (f"synced {len(updated)} copies (run build.py then validate.py)" if args.sync else "workspace matches snapshots/inventory"))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
