#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
INPUT=${1:-/tmp/rime-ice/cn_dicts/base.dict.yaml}
MIN_WEIGHT=${MIN_WEIGHT:-20000}
exec go run ./cmd/clavis-dictgen -in "$INPUT" -blocklist "$ROOT/pkg/core/inputengine/data/blocklist.txt" -out "$ROOT/pkg/core/inputengine/data/common.clvdict" -meta "$ROOT/pkg/core/inputengine/data/common.meta.json" -min-weight "$MIN_WEIGHT" -max-weight 600000
