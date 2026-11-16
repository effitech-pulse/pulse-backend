"""Проверяет ссылки, переносимость, источники и целостность бизнес комплекта."""

from __future__ import annotations

import argparse
import hashlib
import json
import logging
import re
import zipfile
from html.parser import HTMLParser
from pathlib import Path
from urllib.parse import unquote, urlsplit

from build import ARCHIVE_NAME, PACKAGE_NAME, ROOT, package_files, slug

LOGGER = logging.getLogger(__name__)


class Document(HTMLParser):
    """Собирает ссылки и якоря HTML без исполнения содержимого."""

    def __init__(self, source: Path, errors: list[str]) -> None:
        super().__init__()
        self.source = source
        self.errors = errors
        self.ids: set[str] = set()
        self.links: list[str] = []

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        """Регистрирует ссылки, ресурсы и повторяющиеся идентификаторы."""
        values = dict(attrs)
        identifier = values.get("id")
        if identifier:
            if identifier in self.ids:
                self.errors.append(f"Повтор якоря: {self.source.name} #{identifier}")
            self.ids.add(identifier)
        for attribute in ("href", "src"):
            if value := values.get(attribute):
                self.links.append(value)
        if tag in {"script", "link", "img"}:
            for attribute in ("href", "src"):
                if value := values.get(attribute):
                    parts = urlsplit(value)
                    if parts.scheme or parts.netloc:
                        self.errors.append(f"Сетевой ресурс: {value}")


def validate(workspace: Path | None = None) -> None:
    """Проверяет автономный комплект и при необходимости оригиналы источников."""
    errors: list[str] = []
    paths = package_files()
    html_documents: dict[Path, Document] = {}
    markdown_anchors: dict[Path, set[str]] = {}
    links: list[tuple[Path, str]] = []
    for source in paths:
        if source.suffix == ".html":
            document = Document(source, errors)
            document.feed(source.read_text(encoding="utf-8"))
            html_documents[source] = document
            links.extend((source, url) for url in document.links)
        if source.suffix == ".md":
            content = re.sub(r"```.*?```", "", source.read_text(encoding="utf-8"), flags=re.S)
            markdown_anchors[source] = {slug(match[1]) for match in re.finditer(r"^#{1,6}\s+(.+)$", content, re.M)}
            links.extend((source, url) for url in re.findall(r"\[[^\]]+\]\(([^)]+)\)", content))

    optional = 0
    checked = 0
    for source, url in links:
        parts = urlsplit(url)
        if parts.scheme or parts.netloc:
            errors.append(f"Неожиданная внешняя ссылка: {source.name}: {url}")
            continue
        target = (source.parent / unquote(parts.path)).resolve() if parts.path else source
        if target == ROOT.parent / "project-handbook/index.html" and source in {ROOT / "README.md", ROOT / "index.html"}:
            optional += 1
            continue
        if not target.is_relative_to(ROOT):
            errors.append(f"Ссылка за пределы пакета: {source.name}: {url}")
            continue
        checked += 1
        if not target.is_file():
            errors.append(f"Не найден файл: {source.name}: {url}")
            continue
        anchors = html_documents[target].ids if target in html_documents else markdown_anchors.get(target, set())
        if parts.fragment and unquote(parts.fragment) not in anchors:
            errors.append(f"Не найден якорь: {source.name}: {url}")

    entries: list[dict[str, str]] = json.loads((ROOT / "inventory/document-index.json").read_text(encoding="utf-8"))
    identifiers = [entry["id"] for entry in entries]
    if len(identifiers) != len(set(identifiers)):
        errors.append("Повтор идентификатора раздела")
    for entry in entries:
        if not (ROOT / entry["path"]).is_file() or entry["id"] not in html_documents[ROOT / "index.html"].ids:
            errors.append(f"Отсутствует раздел: {entry}")
    portal = (ROOT / "index.html").read_text(encoding="utf-8")
    javascript = (ROOT / "assets/handbook.js").read_text(encoding="utf-8")
    if "fetch(" in javascript or "XMLHttpRequest" in javascript or re.search(r"https?://", javascript + portal):
        errors.append("Неожиданная сеть в автономной странице")

    expected = {path.relative_to(ROOT).as_posix(): path for path in paths}
    checksums: dict[str, str] = {}
    for line in (ROOT / "checksums.sha256").read_text(encoding="utf-8").splitlines():
        digest, name = line.split("  ", 1)
        if name in checksums:
            errors.append(f"Повтор контрольной суммы: {name}")
        checksums[name] = digest
    if set(checksums) != set(expected):
        errors.append("Состав контрольных сумм отличается от папки")
    for name, path in expected.items():
        if hashlib.sha256(path.read_bytes()).hexdigest() != checksums.get(name):
            errors.append(f"Не совпала контрольная сумма: {name}")

    archive_path = ROOT / ARCHIVE_NAME
    if archive_path.exists():
        with zipfile.ZipFile(archive_path) as archive:
            names = archive.namelist()
            archive_files = {f"{PACKAGE_NAME}/{name}": path for name, path in expected.items()}
            archive_files[f"{PACKAGE_NAME}/checksums.sha256"] = ROOT / "checksums.sha256"
            if len(names) != len(set(names)) or set(names) != set(archive_files) or archive.testzip():
                errors.append("Некорректный состав или целостность ZIP")
            for name, path in archive_files.items():
                if name not in names or archive.read(name) != path.read_bytes():
                    errors.append(f"Файл ZIP отличается от папки: {name}")

    registry = json.loads((ROOT / "inventory/sources.json").read_text(encoding="utf-8"))
    for entry in registry["entries"]:
        if not re.fullmatch(r"[a-f0-9]{64}", entry["sha256"]) or not entry.get("supports"):
            errors.append(f"Неполная запись источника: {entry['path']}")
        if workspace:
            original = (workspace / entry["path"]).resolve()
            if not original.is_relative_to(workspace.resolve()) or not original.is_file():
                errors.append(f"Не найден оригинал: {entry['path']}")
            elif hashlib.sha256(original.read_bytes()).hexdigest() != entry["sha256"]:
                errors.append(f"Изменился источник: {entry['path']}")

    if errors:
        for error in errors:
            LOGGER.error(error)
        raise SystemExit(1)
    LOGGER.info(
        "OK: %d разделов, %d ссылок и якорей, %d контрольных сумм, %d источников; %d дополнительных ссылок на соседний справочник; ZIP %s; оригиналы %s",
        len(entries), checked, len(checksums), len(registry["entries"]), optional,
        "совпадает с папкой" if archive_path.exists() else "не требуется в распакованном комплекте",
        "сверены" if workspace else "не требуются для автономной проверки",
    )


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO, format="%(message)s")
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workspace", type=Path, help="Корень repository-split для проверки оригиналов")
    validate(parser.parse_args().workspace)
