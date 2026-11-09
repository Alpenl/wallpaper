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
        if path.name.endswith('_test.go'):
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
go test -race ./cmd/... ./internal/... ./scripts/...
go vet -unsafeptr=false ./cmd/... ./internal/... ./scripts/...
CGO_ENABLED=0 go build ./cmd/...
# Documentation cleanup and link checks are deferred to M4.
if [ "${CROSS_BUILD:-0}" = 1 ]; then
  bash scripts/build.sh
fi
