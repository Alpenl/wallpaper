#!/usr/bin/env python3
"""检查实际暂存的提交总结；也为 Claude 会话提供只读配置提示。"""
from __future__ import annotations

import argparse
import datetime
import json
import re
import subprocess
import sys
from pathlib import Path

SUMMARY = "docs/COMMIT_SUMMARIES.md"
ENTRY = re.compile(r"^## (\d{4}-\d{2}-\d{2}) \| (\S[^\r\n]*)$", re.MULTILINE)
FIELDS = ("目的", "改动", "验证", "限制")


class SummaryError(Exception):
    pass


def git(*args: str, check: bool = True) -> subprocess.CompletedProcess:
    return subprocess.run(["git", *args], capture_output=True, check=check)


def blob(revision: str) -> str:
    # 只读取 Git 对象，不读取可能尚未暂存的工作区文件。
    result = git("show", f"{revision}:{SUMMARY}", check=False)
    if result.returncode:
        # 文件不存在合法；其他 Git 故障不能伪装成空历史。
        tree = git("ls-tree", "--name-only", revision, "--", SUMMARY)
        if tree.stdout.strip():
            raise SummaryError(f"Cannot read {revision}:{SUMMARY}")
        return ""
    return result.stdout.decode("utf-8").replace("\r\n", "\n")


def entries(text: str) -> list[re.Match]:
    return list(ENTRY.finditer(text))


def validate(previous: str, current: str, inherited: set[str], required: bool) -> None:
    if not current.startswith(previous):
        raise SummaryError(f"Append to {SUMMARY}; do not rewrite or delete existing records.")
    matches = entries(current)
    titles = [m.group(0) for m in matches]
    if len(set(titles)) != len(titles):
        raise SummaryError("Each summary heading must be unique.")
    new = [m for m in matches if m.start() >= len(previous) and m.group(0) not in inherited]
    if required and not new:
        raise SummaryError(f"Code/config/resource changes require a new staged entry in {SUMMARY}.")
    if current != previous and not new:
        raise SummaryError("Summary changes must append a new entry, not just whitespace or old text.")
    for match in new:
        datetime.date.fromisoformat(match.group(1))
        index = matches.index(match)
        end = matches[index + 1].start() if index + 1 < len(matches) else len(current)
        section = current[match.end():end]
        for field in FIELDS:
            if not re.search(rf"^- {field}：[^\S\r\n]*\S[^\r\n]*$", section, re.MULTILINE):
                raise SummaryError(f"{match.group(0)} needs a non-empty '- {field}：' line.")


def check_staged() -> None:
    # --no-renames 将重命名视为删除+新增，避免路径解析歧义。
    paths = git("diff", "--cached", "--name-only", "--no-renames", "-z").stdout.split(b"\0")
    paths = [p.decode("utf-8") for p in paths if p]
    if not paths:
        return
    required = any(not p.lower().endswith(".md") for p in paths)
    if not required and SUMMARY not in paths:
        return
    head = git("rev-parse", "--verify", "HEAD", check=False)
    previous = blob("HEAD") if head.returncode == 0 else ""
    inherited = {m.group(0) for m in entries(previous)}
    merge_path = Path(git("rev-parse", "--git-path", "MERGE_HEAD").stdout.decode().strip())
    if merge_path.exists():
        for parent in merge_path.read_text(encoding="ascii").splitlines():
            inherited.update(m.group(0) for m in entries(blob(parent)))
    mode = git("ls-files", "--stage", "--", SUMMARY).stdout
    if not mode.startswith((b"100644 ", b"100755 ")):
        raise SummaryError(f"Stage a regular UTF-8 file at {SUMMARY}.")
    current = git("show", f":{SUMMARY}").stdout.decode("utf-8").replace("\r\n", "\n")
    validate(previous, current, inherited, required)


def claude_context() -> None:
    # 不修改配置；Git 原生钩子才是提交时读取最终暂存区的检查点。
    configured = git("config", "--get", "core.hooksPath", check=False).stdout.decode().strip()
    active = configured == ".githooks"
    message = (
        f"每次代码提交前必须在 {SUMMARY} 末尾追加总结并一同暂存。"
        + ("本仓库已配置 .githooks 提交检查。" if active else
           "本仓库尚未配置 core.hooksPath=.githooks；请依工程规则检查现有钩子后启用，不要覆盖已有配置。")
        + "钩子不写总结、不自动暂存或提交；合并代码需 --no-commit 后追加独立合并总结。"
    )
    print(json.dumps({"hookSpecificOutput": {
        "hookEventName": "SessionStart", "additionalContext": message,
    }}, ensure_ascii=True))


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--claude-context", action="store_true")
    args = parser.parse_args()
    try:
        if args.claude_context:
            claude_context()
        else:
            check_staged()
    except (SummaryError, subprocess.CalledProcessError, OSError, UnicodeError, ValueError) as exc:
        print(f"Commit summary check failed: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
