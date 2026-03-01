#!/usr/bin/env bash

set -euo pipefail

OUTPUT_DIR="${1:-output}"
BINARY_NAME="${2:-sub2api_simple}"

mkdir -p "${OUTPUT_DIR}"

BIN_PATH="${OUTPUT_DIR}/${BINARY_NAME}"
echo "Building -> ${BIN_PATH}"
go build -o "${BIN_PATH}" .
echo "Build completed: ${BIN_PATH}"

