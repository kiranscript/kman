#!/usr/bin/env bash
# Installs the SDKs the e2e tests pin, by lockfile and by hash. Run scripts are off:
# nothing here needs one. Without this, TestOfficialSDKs skips (see e2e/sdk_test.go).
set -euo pipefail
cd "$(dirname "$0")"
(cd node && npm ci --ignore-scripts --no-audit --no-fund)
python3 -m venv python/.venv
python/.venv/bin/python -m pip install --quiet --disable-pip-version-check --require-hashes --only-binary :all: -r python/requirements.txt
