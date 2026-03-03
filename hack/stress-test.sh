#!/usr/bin/env bash
set -uo pipefail

RUNS="${1:-100}"
if ! [[ "$RUNS" =~ ^[0-9]+$ ]]; then
    echo "ERROR: RUNS must be a positive integer, got: $RUNS"
    exit 1
fi
DIR="$(cd "$(dirname "$0")/.." && pwd)"
LOGDIR="$DIR/hack/stress-test-logs"
SUMMARY="$LOGDIR/summary.csv"

if [ -d "$LOGDIR" ]; then
    echo "Previous test logs found in $LOGDIR"
    read -r -p "Delete previous results? [y/N] " confirm
    if [[ "$confirm" =~ ^[Yy]$ ]]; then
        rm -rf "$LOGDIR"
    else
        echo "Aborting."
        exit 0
    fi
fi
mkdir -p "$LOGDIR"

echo "run,status,duration,failed_test,reason" > "$SUMMARY"

cd "$DIR"
export KUBEBUILDER_ASSETS="$DIR/bin/k8s/1.33.0-darwin-arm64"

if [ ! -d "$KUBEBUILDER_ASSETS" ]; then
    echo "ERROR: KUBEBUILDER_ASSETS directory not found: $KUBEBUILDER_ASSETS"
    echo "Run 'make envtest' first."
    exit 1
fi

echo "Running $RUNS iterations, logs in $LOGDIR"
echo "KUBEBUILDER_ASSETS=$KUBEBUILDER_ASSETS"
echo ""

pass=0
fail=0

for i in $(seq 1 "$RUNS"); do
    logfile="$LOGDIR/run-$(printf '%03d' "$i").log"
    start=$(date +%s)

    if go test ./internal/controller/ -race -count=1 -timeout 180s > "$logfile" 2>&1; then
        duration=$(( $(date +%s) - start ))
        echo "$i,pass,${duration}s,," >> "$SUMMARY"
        pass=$((pass + 1))
        printf "[%3d/%d] PASS (%ds)\n" "$i" "$RUNS" "$duration"
    else
        duration=$(( $(date +%s) - start ))
        failed_test=""
        reason=""

        # Strip ANSI escape codes from log for reliable extraction
        clean=$(sed 's/\x1b\[[0-9;]*m//g' "$logfile" 2>/dev/null) || true

        # Extract failed test name from Ginkgo's "Summarizing N Failures" section
        failed_test=$(printf '%s' "$clean" | sed -n 's/^[[:space:]]*\[FAIL\] \(.*\)/\1/p' 2>/dev/null | head -1) || true

        # Extract failure reason — prefer "[FAILED] Timed out" or "Expected/to equal", then "Messages:", then generic
        reason=$(printf '%s' "$clean" | grep -E '^\s*\[FAILED\] Timed out|Expected$|to equal' 2>/dev/null | tr '\n' ' ' | sed 's/[[:space:]]*$//' | cut -c1-200) || true
        if [ -z "$reason" ]; then
            reason=$(printf '%s' "$clean" | sed -n 's/^[[:space:]]*Messages:[[:space:]]*\(.*\)/\1/p' 2>/dev/null | tr '\n' ' ' | sed 's/[[:space:]]*$//' | cut -c1-200) || true
        fi
        if [ -z "$reason" ]; then
            reason=$(printf '%s' "$clean" | grep -E 'panic|timeout|DATA RACE' 2>/dev/null | head -1 | cut -c1-200) || true
        fi

        # Sanitize for CSV
        failed_test="${failed_test//,/;}"
        reason="${reason//,/;}"
        echo "$i,fail,${duration}s,\"$failed_test\",\"$reason\"" >> "$SUMMARY"
        fail=$((fail + 1))
        printf "[%3d/%d] FAIL (%ds) - %s\n" "$i" "$RUNS" "$duration" "$failed_test"
    fi
done

echo ""
echo "========================================="
echo "Results: $pass passed, $fail failed out of $RUNS runs"
echo "Failure rate: $(awk -v f="$fail" -v r="$RUNS" 'BEGIN {printf "%.1f", (f/r)*100}')%"
echo "Summary: $SUMMARY"
echo "========================================="
