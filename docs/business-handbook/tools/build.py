"""Собирает автономные HTML и ZIP бизнес справочника из Markdown."""

from __future__ import annotations

import hashlib
import html
import json
import logging
import re
import zipfile
from pathlib import Path
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]
ARCHIVE_NAME = "pulse-business-documentation.zip"
PACKAGE_NAME = "pulse-business-documentation"
LOGGER = logging.getLogger(__name__)


def slug(value: str) -> str:
    """Возвращает якорь заголовка с сохранением кириллицы."""
    return re.sub(r"[^\w\- ]", "", value.lower()).strip().replace(" ", "-")


def package_files() -> list[Path]:
    """Возвращает разрешённые файлы пакета без архива и временных файлов."""
    files: list[Path] = []
    for path in sorted(ROOT.rglob("*")):
        parts = path.relative_to(ROOT).parts
        if path.is_symlink() or any(
            part.startswith(".env")
            or part in {".git", ".venv", ".aws", ".ssh", "workspace-data", "data"}
            for part in parts
        ):
            raise ValueError(f"Недопустимый путь пакета: {path.relative_to(ROOT)}")
        if (
            path.is_file()
            and path.name not in {ARCHIVE_NAME, "checksums.sha256", ".DS_Store"}
            and "__pycache__" not in parts
            and path.suffix != ".pyc"
        ):
            files.append(path)
    return files


def rewrite(url: str, source: Path, documents: dict[Path, str]) -> str:
    """Преобразует ссылку на раздел Markdown в якорь единой HTML страницы."""
    parts = urlsplit(html.unescape(url))
    if parts.scheme or parts.netloc:
        return url
    target = (source.parent / unquote(parts.path)).resolve() if parts.path else source
    if target in documents:
        return "#" + documents[target] + ("-" + parts.fragment if parts.fragment else "")
    if target.is_relative_to(ROOT):
        return target.relative_to(ROOT).as_posix() + ("#" + parts.fragment if parts.fragment else "")
    if target == ROOT.parent / "project-handbook/index.html":
        return "../project-handbook/index.html"
    raise ValueError(f"Ссылка за пределы комплекта: {source.name}: {url}")


def inline(value: str, source: Path, documents: dict[Path, str]) -> str:
    """Экранирует текст и обрабатывает код, ссылки и выделение."""
    tokens: list[str] = []

    def token(rendered: str) -> str:
        tokens.append(rendered)
        return f"\x00{len(tokens) - 1}\x00"

    value = re.sub(r"`([^`]+)`", lambda match: token("<code>" + html.escape(match[1]) + "</code>"), value)
    value = re.sub(
        r"\[([^\]]+)\]\(([^)]+)\)",
        lambda match: token(
            '<a href="' + html.escape(rewrite(match[2], source, documents), quote=True)
            + '">' + html.escape(match[1]) + "</a>"
        ),
        value,
    )
    value = html.escape(value)
    value = re.sub(r"\*\*([^*]+)\*\*", r"<strong>\1</strong>", value)
    for index, rendered in enumerate(tokens):
        value = value.replace(f"\x00{index}\x00", rendered)
    return value


def render(source: Path, identifier: str, documents: dict[Path, str]) -> str:
    """Преобразует используемое подмножество Markdown в безопасный HTML."""
    lines = source.read_text(encoding="utf-8").splitlines()
    output: list[str] = []
    index = 0

    def text(value: str) -> str:
        return inline(value, source, documents)

    while index < len(lines):
        line = lines[index]
        if not line.strip():
            index += 1
            continue
        heading = re.match(r"^(#{1,6})\s+(.+)", line)
        if heading:
            level = len(heading[1])
            output.append(f'<h{level} id="{identifier}-{slug(heading[2])}">{text(heading[2])}</h{level}>')
            index += 1
            continue
        if line.startswith("```"):
            code: list[str] = []
            index += 1
            while index < len(lines) and not lines[index].startswith("```"):
                code.append(lines[index])
                index += 1
            output.append("<pre><code>" + html.escape("\n".join(code)) + "</code></pre>")
            index += 1
            continue
        if line.startswith("|") and index + 1 < len(lines) and re.fullmatch(r"[| :\-]+", lines[index + 1]):
            header = [text(cell.strip()) for cell in line.strip().strip("|").split("|")]
            index += 2
            rows: list[list[str]] = []
            while index < len(lines) and lines[index].startswith("|"):
                rows.append([text(cell.strip()) for cell in lines[index].strip().strip("|").split("|")])
                index += 1
            output.append(
                '<div class="table-scroll"><table><thead><tr>'
                + "".join("<th scope=\"col\">" + cell + "</th>" for cell in header)
                + "</tr></thead><tbody>"
                + "".join("<tr>" + "".join("<td>" + cell + "</td>" for cell in row) + "</tr>" for row in rows)
                + "</tbody></table></div>"
            )
            continue
        ordered = bool(re.match(r"^\d+\.\s+", line))
        if ordered or re.match(r"^[-*]\s+", line):
            tag = "ol" if ordered else "ul"
            pattern = r"^\d+\.\s+" if ordered else r"^[-*]\s+"
            items: list[str] = []
            while index < len(lines) and re.match(pattern, lines[index]):
                items.append("<li>" + text(re.sub(pattern, "", lines[index])) + "</li>")
                index += 1
            output.append(f"<{tag}>" + "".join(items) + f"</{tag}>")
            continue
        if line.startswith("> "):
            output.append("<blockquote>" + text(line[2:]) + "</blockquote>")
            index += 1
            continue
        paragraph = [line]
        index += 1
        while index < len(lines) and lines[index].strip() and not re.match(r"^(#|```|\||[-*] |\d+\. )", lines[index]):
            paragraph.append(lines[index])
            index += 1
        output.append("<p>" + text(" ".join(paragraph)) + "</p>")
    return "\n".join(output)


def build() -> None:
    """Собирает страницу, контрольные суммы и переносимый архив."""
    package_files()
    entries: list[dict[str, str]] = json.loads((ROOT / "inventory/document-index.json").read_text(encoding="utf-8"))
    documents = {(ROOT / entry["path"]).resolve(): entry["id"] for entry in entries}
    nav = "".join(
        f'<a href="#{entry["id"]}" data-id="{entry["id"]}">{html.escape(entry["title"])}</a>'
        for entry in entries
    )
    articles = "".join(
        f'<article id="{entry["id"]}" data-title="{html.escape(entry["title"], quote=True)}"'
        + ("" if entry["id"] == "home" else " hidden") + ">"
        + render((ROOT / entry["path"]).resolve(), entry["id"], documents) + "</article>"
        for entry in entries
    )
    page = f'''<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Бизнес документация · Пульс скважины</title><link rel="icon" href="assets/pulse-symbol.svg" type="image/svg+xml">
<link rel="stylesheet" href="assets/handbook.css"></head><body>
<a class="skip-link" href="#main">К содержимому</a>
<header><img src="assets/pulse-symbol.svg" alt=""><div><strong>Пульс скважины</strong>
<small>Бизнес документация · 25 октября 2026</small></div>
<div class="header-actions"><a href="README.md">Исходники Markdown</a>
<button id="print" type="button">Печать всех разделов</button></div></header>
<div class="layout"><aside><label for="search">Поиск по содержимому</label>
<input id="search" type="search" placeholder="Например: пилот, эффект, тревога" autocomplete="off">
<span id="search-status" role="status" aria-live="polite"></span>
<nav aria-label="Разделы бизнес документации">{nav}</nav>
<div id="no-results" class="empty" hidden>Совпадений нет. Измените или очистите запрос.</div></aside>
<main id="main" tabindex="-1">{articles}<p class="footer">Сверка документов на 25.10.2026 ·
текущие возможности и цели различаются · условия пилота и коммерческие показатели согласуются отдельно</p></main></div>
<script src="assets/handbook.js"></script></body></html>'''
    (ROOT / "index.html").write_text(page, encoding="utf-8")
    paths = package_files()
    (ROOT / "checksums.sha256").write_text(
        "".join(hashlib.sha256(path.read_bytes()).hexdigest() + "  " + path.relative_to(ROOT).as_posix() + "\n" for path in paths),
        encoding="utf-8",
    )
    with zipfile.ZipFile(ROOT / ARCHIVE_NAME, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        for path in [*paths, ROOT / "checksums.sha256"]:
            archive.write(path, Path(PACKAGE_NAME) / path.relative_to(ROOT))
    LOGGER.info("HTML: %d разделов; ZIP: %d байт", len(entries), (ROOT / ARCHIVE_NAME).stat().st_size)


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO, format="%(message)s")
    build()
