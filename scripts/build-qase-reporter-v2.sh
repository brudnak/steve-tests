#!/bin/bash
# Builds the Qase reporter into ./reporter for the current platform.
set -euo pipefail

cd "$(dirname "$0")/.."

if [[ -z "${QASE_TEST_RUN_ID:-}" && -z "${TEST_RUN_NAME:-}" ]]; then
  echo "No Qase test run ID or test run name provided"
fi
if [[ -z "${QASE_PROJECT_ID:-}" ]]; then
  echo "No Qase project ID provided; the reporter defaults to RM"
fi

echo "Building Qase reporter binary"
CGO_ENABLED=0 go build -buildvcs=false -o reporter ./pipeline/qase/reporter-v2
