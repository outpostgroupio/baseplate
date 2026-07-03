#!/usr/bin/env bash
# Regenerates Open Cloud wire types from the vendored per-service specs.
# Run from anywhere: bash internal/provider/roblox/opencloud/regen.sh
#
# Specs live in specs/ and are vendored from Roblox/creator-docs
# (content/en-us/reference/cloud/*). The large unified openapi.json is used
# for universe-v2 extraction (Phase 1B) and is not committed.
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR"
TOOL="github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.4.1"
mkdir -p gen/assets gen/products
go run "$TOOL" -config assets.cfg.yaml specs/assets.json
go run "$TOOL" -config products.cfg.yaml specs/developer-products.json
gofmt -w gen
echo "regenerated gen/assets and gen/products from vendored specs"
