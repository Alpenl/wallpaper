#!/bin/sh
# SessionStart 只读提示，不安装钩子、不改 Git 配置。
cd "${CLAUDE_PROJECT_DIR:?CLAUDE_PROJECT_DIR is required}" || exit 1
for python in python3 python; do
    if "$python" -c 'import sys; sys.exit(sys.version_info < (3, 9))' >/dev/null 2>&1; then
        exec "$python" scripts/check-commit-summary.py --claude-context
    fi
done
printf '%s\n' 'Commit summary guidance requires Python 3.9+ (python3 or python).' >&2
exit 1
