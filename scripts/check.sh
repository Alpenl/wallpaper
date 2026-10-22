#!/usr/bin/env bash
# Reproducible production checks; historical prototype results are separate.
set -euo pipefail
cd "$(dirname "$0")/.."
# Physical lines, including an unterminated final line; scan every GOOS.
python3 - <<'PY_SIZE'
from pathlib import Path
import sys
oversized = []
for root in ('cmd', 'internal'):
    for path in Path(root).rglob('*.go'):
        if path.name.endswith('_test.go') or path.as_posix() == 'internal/ui/i18n.go':
            continue
        data = path.read_bytes()
        lines = data.count(b'\n') + bool(data and not data.endswith(b'\n'))
        if lines > 2000:
            oversized.append(f'{path}: {lines} physical lines (limit 2000)')
if oversized:
    sys.exit('\n'.join(oversized))
PY_SIZE
bash scripts/check-format.sh
go run ./scripts/rulecheck
go test -race ./...
go vet -unsafeptr=false ./...
CGO_ENABLED=0 go build ./cmd/...
python3 scripts/check-docs.py
# The separate ui branch is design-only. This master-based application and its
# behavior checks are exclusively native Go/MyGo; no React runtime is included.
if [ "${CROSS_BUILD:-0}" = 1 ]; then
  bash scripts/build.sh
fi
