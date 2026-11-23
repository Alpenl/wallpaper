#!/usr/bin/env python3
"""SessionStart read-only hook wrapper (cross-platform without requiring sh)."""
from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path


def main() -> int:
    if sys.version_info < (3, 9):
        print(
            "Commit summary guidance requires Python 3.9+ (python3 or python).",
            file=sys.stderr,
        )
        return 1
    project_dir = os.environ.get("CLAUDE_PROJECT_DIR")
    if not project_dir:
        project_dir = str(Path(__file__).resolve().parent.parent)
    root = Path(project_dir)
    script = root / "scripts" / "check-commit-summary.py"
    result = subprocess.run(
        [sys.executable, str(script), "--claude-context"],
        cwd=root,
    )
    return result.returncode


if __name__ == "__main__":
    raise SystemExit(main())
