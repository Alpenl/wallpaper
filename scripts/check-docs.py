#!/usr/bin/env python3
"""Offline checks for this repository's Markdown links and implementation IDs.

Checks the native master worktree README and docs/**/*.md. Does not fetch external URLs,
assert code correctness, or interpret historical inline-code paths as links.
"""
from __future__ import annotations

import re
import sys
import tempfile
import unicodedata
from pathlib import Path
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parent.parent
LINK = re.compile(r"!?\[[^\]\n]*\]\(\s*(<[^>]+>|[^\s)]+)(?:\s+[\"'][^\n]*?[\"'])?\s*\)")
REFERENCE = re.compile(r"^\s{0,3}\[[^\]]+\]:\s*(<[^>]+>|\S+)", re.MULTILINE)


def prose(text: str) -> str:
    """Exclude fenced and inline code; preserve line counts for diagnostics."""
    result, fence = [], None
    for line in text.splitlines():
        marker = re.match(r"^\s{0,3}(`{3,}|~{3,})", line)
        if marker:
            token = marker.group(1)
            if fence is None:
                fence = token
            elif token[0] == fence[0] and len(token) >= len(fence):
                fence = None
            result.append("")
        elif fence is None:
            result.append(re.sub(r"(`+).*?\1", "", line))
        else:
            result.append("")
    return "\n".join(result)


def anchors(text: str) -> set[str]:
    """GitHub-style heading slugs for the headings used in this repository."""
    result, counts, fence = set(), {}, None
    for line in text.splitlines():
        marker = re.match(r"^\s{0,3}(`{3,}|~{3,})", line)
        if marker:
            token = marker.group(1)
            if fence is None:
                fence = token
            elif token[0] == fence[0] and len(token) >= len(fence):
                fence = None
            continue
        if fence is not None:
            continue
        match = re.match(r"^\s{0,3}#{1,6}\s+(.+?)\s*#*\s*$", line)
        if not match:
            continue
        title = re.sub(r"<[^>]*>", "", match.group(1))
        title = re.sub(r"\[([^]]+)\]\([^)]*\)", r"\1", title).lower()
        slug = "".join(c for c in title if c in "-_ " or unicodedata.category(c)[0] in "LN")
        slug = slug.replace(" ", "-")
        number = counts.get(slug, 0)
        counts[slug] = number + 1
        result.add(f"{slug}-{number}" if number else slug)
    result.update(re.findall(r'''\bid=["']([^"']+)["']''', text))
    return result


def check_links(path: Path, root: Path) -> tuple[int, list[str]]:
    text = prose(path.read_text(encoding="utf-8"))
    checked, errors = 0, []
    for match in list(LINK.finditer(text)) + list(REFERENCE.finditer(text)):
        raw = match.group(1).strip("<>")
        url = urlsplit(raw)
        if url.scheme or url.netloc or raw.startswith("//"):
            continue
        target = (path.parent / unquote(url.path)).resolve() if url.path else path.resolve()
        line = text.count("\n", 0, match.start()) + 1
        where = f"{path.relative_to(root)}:{line}"
        checked += 1
        try:
            target.relative_to(root)
        except ValueError:
            errors.append(f"{where}: local link escapes repository: {raw}")
            continue
        if not target.exists():
            errors.append(f"{where}: missing target: {raw}")
            continue
        if url.fragment and target.is_file() and target.suffix.lower() == ".md":
            if unquote(url.fragment) not in anchors(target.read_text(encoding="utf-8")):
                errors.append(f"{where}: missing Markdown anchor: {raw}")
    return checked, errors


def self_test() -> None:
    with tempfile.TemporaryDirectory(prefix="wallpaper-docs-") as directory:
        root = Path(directory)
        (root / "target.md").write_text("# 中文 标题\n# Repeat\n# Repeat\n", encoding="utf-8")
        source = root / "README.md"
        source.write_text(
            '[中文](target.md#中文-标题)\n[再次](target.md#repeat-1)\n'
            '[ref]: target.md\n`[ignored](missing.md)`\n'
            '```md\n[ignored](missing.md)\n```\n[external](https://example.org/missing)\n',
            encoding="utf-8",
        )
        count, errors = check_links(source, root)
        assert count == 3 and not errors, (count, errors)
        source.write_text('[bad](missing.md)\n[anchor](target.md#absent)\n[out](../outside.md)\n', encoding="utf-8")
        count, errors = check_links(source, root)
        assert count == 3 and len(errors) == 3, (count, errors)
    print("PASS: link checker self-test")


def main() -> int:
    if sys.argv[1:] == ["--self-test"]:
        self_test()
        return 0
    if sys.argv[1:]:
        print("usage: python3 scripts/check-docs.py [--self-test]", file=sys.stderr)
        return 2
    paths = [ROOT / "README.md", *sorted((ROOT / "docs").rglob("*.md"))]
    checked, errors = 0, []
    for path in paths:
        if not path.is_file():
            errors.append(f"missing required document: {path.relative_to(ROOT)}")
            continue
        count, problems = check_links(path, ROOT)
        checked += count
        errors.extend(problems)
    status = ROOT / "docs/IMPLEMENTATION_STATUS.md"
    if status.is_file():
        text = status.read_text(encoding="utf-8")
        for prefix, maximum in [("F", 26), ("UI", 7)]:
            ids = re.findall(rf"^\| ({prefix}-\d{{2}}) \|", text, re.MULTILINE)
            expected = [f"{prefix}-{n:02}" for n in range(1, maximum + 1)]
            if ids != expected:
                errors.append(f"docs/IMPLEMENTATION_STATUS.md: expected each {prefix} row once, in order: {expected}; got {ids}")
    else:
        errors.append("missing required document: docs/IMPLEMENTATION_STATUS.md")
    if errors:
        print("FAIL: documentation checks")
        print("\n".join(errors))
        return 1
    print(f"PASS: {len(paths)} Markdown files, {checked} local links, F-01..F-26 and UI-01..UI-07")
    print("External URLs, historical code paths, and runtime correctness were not checked.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
