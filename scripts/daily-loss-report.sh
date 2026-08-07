#!/usr/bin/env bash
# Versioned convenience runner. Its dated report output remains ignored.
set -euo pipefail
cd "$(dirname "$0")/.."
report_dir="local-analysis"
report_path="$report_dir/daily-loss-$(date +%F).txt"
mkdir -p "$report_dir"
./go.sh daily-loss-report -max-daily-loss 2300 "$@" | tee "$report_path"
printf 'Saved %s\n' "$report_path"
