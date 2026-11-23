#!/usr/bin/env python3
"""Offline checks for this repository's Markdown links and implementation IDs.

Checks all tracked Markdown files across the repository. Does not fetch external URLs,
assert code correctness, or interpret historical inline-code paths as links.
"""
from __future__ import annotations

import posixpath
import re
import subprocess
import sys
import tempfile
import unicodedata
from pathlib import Path
from urllib.parse import unquote, urlsplit

LINK = re.compile(r"!?\[[^\]\n]*\]\(\s*(<[^>]+>|[^\s)]+)(?:\s+[\"'][^\n]*?[\"'])?\s*\)")
REFERENCE = re.compile(r"^\s{0,3}\[[^\]]+\]:\s*(<[^>]+>|\S+)", re.MULTILINE)
IMG_SRC = re.compile(
    r"""<img\b[^>]*?\bsrc\s*=\s*(?:"([^"]+)"|'([^']+)'|([^\s>]+))""",
    re.IGNORECASE,
)


def repo_root() -> Path:
    result = subprocess.run(
        ["git", "rev-parse", "--show-toplevel"],
        cwd=Path(__file__).resolve().parent,
        capture_output=True,
        text=True,
        check=False,
    )
    if result.returncode == 0 and result.stdout.strip():
        return Path(result.stdout.strip()).resolve()
    return Path(__file__).resolve().parent.parent.resolve()


def markdown_files(root: Path) -> list[Path]:
    result = subprocess.run(
        ["git", "ls-files", "-z", "*.md"],
        cwd=root,
        capture_output=True,
        check=False,
    )
    if result.returncode == 0:
        rels = [p.decode("utf-8") for p in result.stdout.split(b"\0") if p]
        return sorted(root / rel for rel in rels)
    return sorted(p for p in root.rglob("*.md") if ".git" not in p.parts)


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


def exact_case_exists(root: Path, rel_posix: str, cache: dict[Path, set[str]]) -> bool:
    """Verify every path component matches on-disk case, even on case-insensitive OSes."""
    if not rel_posix or rel_posix == ".":
        return True
    cur = root
    for part in rel_posix.split("/"):
        if not part or part == ".":
            continue
        entries = cache.get(cur)
        if entries is None:
            try:
                entries = {entry.name for entry in cur.iterdir()}
            except OSError:
                return False
            cache[cur] = entries
        if part not in entries:
            return False
        cur = cur / part
    return True


def check_links(path: Path, root: Path, dir_cache: dict[Path, set[str]] | None = None) -> tuple[int, list[str]]:
    if dir_cache is None:
        dir_cache = {}
    root = root.resolve()
    resolved_path = path.resolve()
    rel_dir = resolved_path.relative_to(root).parent.as_posix()
    text = prose(resolved_path.read_text(encoding="utf-8"))
    checked, errors = 0, []
    candidates: list[tuple[int, str, bool]] = []
    for match in list(LINK.finditer(text)) + list(REFERENCE.finditer(text)):
        candidates.append((match.start(), match.group(1).strip("<>"), True))
    for match in IMG_SRC.finditer(text):
        src = match.group(1) or match.group(2) or match.group(3)
        candidates.append((match.start(), src.strip(), False))
    candidates.sort(key=lambda item: item[0])

    for pos, raw, check_anchor in candidates:
        url = urlsplit(raw)
        if url.scheme or url.netloc or raw.startswith("//"):
            continue
        decoded_path = unquote(url.path)
        line = text.count("\n", 0, pos) + 1
        where = f"{resolved_path.relative_to(root)}:{line}"
        checked += 1
        if decoded_path:
            if decoded_path.startswith("/"):
                rel_norm = posixpath.normpath(decoded_path.lstrip("/"))
                candidate_path = root / rel_norm
            else:
                joined = posixpath.join("" if rel_dir == "." else rel_dir, decoded_path)
                rel_norm = posixpath.normpath(joined)
                candidate_path = resolved_path.parent / decoded_path
            target = candidate_path.resolve()
        else:
            rel_norm = resolved_path.relative_to(root).as_posix()
            target = resolved_path
        try:
            target.relative_to(root)
        except ValueError:
            errors.append(f"{where}: local link escapes repository: {raw}")
            continue
        if rel_norm == ".." or rel_norm.startswith("../"):
            errors.append(f"{where}: local link escapes repository: {raw}")
            continue
        if not target.exists():
            errors.append(f"{where}: missing target: {raw}")
            continue
        if not exact_case_exists(root, rel_norm, dir_cache):
            errors.append(f"{where}: case mismatch for target: {raw}")
            continue
        if check_anchor and url.fragment and target.is_file() and target.suffix.lower() == ".md":
            if unquote(url.fragment) not in anchors(target.read_text(encoding="utf-8")):
                errors.append(f"{where}: missing Markdown anchor: {raw}")
    return checked, errors


def self_test() -> None:
    with tempfile.TemporaryDirectory(prefix="wallpaper-docs-") as directory:
        root = Path(directory).resolve()
        (root / "target.md").write_text("# 中文 标题\n# Repeat\n# Repeat\n", encoding="utf-8")
        (root / "pic.png").write_bytes(b"png")
        source = root / "README.md"
        source.write_text(
            '[中文](target.md#中文-标题)\n[再次](target.md#repeat-1)\n'
            '[ref]: target.md\n<img src="pic.png" alt="ok">\n'
            '`[ignored](missing.md)`\n'
            '```md\n[ignored](missing.md)\n<img src="missing.png">\n```\n'
            '[external](https://example.org/missing)\n',
            encoding="utf-8",
        )
        count, errors = check_links(source, root)
        assert count == 4 and not errors, (count, errors)
        source.write_text(
            '[bad](missing.md)\n'
            '[anchor](target.md#absent)\n'
            '[out](../outside.md)\n'
            '[wrong-case](Target.md)\n'
            '<img src="missing.png">\n'
            '<img src="Pic.png">\n',
            encoding="utf-8",
        )
        count, errors = check_links(source, root)
        assert count == 6 and len(errors) == 6, (count, errors)
    print("PASS: link checker self-test")


def main() -> int:
    if sys.argv[1:] == ["--self-test"]:
        self_test()
        return 0
    if sys.argv[1:]:
        print("usage: python3 scripts/check-docs.py [--self-test]", file=sys.stderr)
        return 2
    root = repo_root()
    paths = markdown_files(root)
    dir_cache: dict[Path, set[str]] = {}
    checked, errors = 0, []
    for required in (root / "README.md", root / "docs/IMPLEMENTATION_STATUS.md"):
        if not required.is_file():
            errors.append(f"missing required document: {required.relative_to(root)}")
    for path in paths:
        if not path.is_file():
            errors.append(f"missing tracked document: {path.relative_to(root)}")
            continue
        count, problems = check_links(path, root, dir_cache)
        checked += count
        errors.extend(problems)
    status = root / "docs/IMPLEMENTATION_STATUS.md"
    if status.is_file():
        text = status.read_text(encoding="utf-8")
        for prefix, maximum in [("F", 26), ("UI", 7)]:
            ids = re.findall(rf"^\| ({prefix}-\d{{2}}) \|", text, re.MULTILINE)
            expected = [f"{prefix}-{n:02}" for n in range(1, maximum + 1)]
            if ids != expected:
                errors.append(f"docs/IMPLEMENTATION_STATUS.md: expected each {prefix} row once, in order: {expected}; got {ids}")
    if errors:
        print("FAIL: documentation checks")
        print("\n".join(errors))
        return 1
    print(f"PASS: {len(paths)} Markdown files, {checked} local links, F-01..F-26 and UI-01..UI-07")
    print("External URLs, historical code paths, and runtime correctness were not checked.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
