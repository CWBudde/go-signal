#!/usr/bin/env bash

set -euo pipefail

# Decimal point in the percentages, whatever the caller's locale.
export LC_ALL=C

# Generates coverage-results.md (repo root) from coverage.out, as produced by
# `just test-coverage`. CI posts it to the step summary and as a PR comment; it
# also works locally.
#
# The profile is recorded with -coverpkg (all packages), so every package's test binary
# reports every package and each block appears once per test binary. `go tool
# cover -func` merges those for the total; the per-package table merges them
# the same way (a block counts as covered if any test binary hit it).

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

PROFILE="${1:-coverage.out}"
OUT="coverage-results.md"

if [[ ! -f ${PROFILE} ]]; then
	echo "coverage-report: ${PROFILE} not found — run 'just test-coverage' first" >&2
	exit 1
fi

MODPATH="$(go list -m)"
TOTAL="$(go tool cover -func="${PROFILE}" | awk '/^total:/ { print $NF }')"

{
	echo "## Coverage: ${TOTAL}"
	echo ""
	echo "<details>"
	echo "<summary>Per-package statement coverage</summary>"
	echo ""
	echo "| Package | Coverage | Statements |"
	echo "|---------|----------|------------|"
	# Block format: <importpath>/<file>.go:<range> <numStmts> <count>
	awk -v modp="${MODPATH}" '
        NR == 1 && $1 == "mode:" { next }
        {
            stmts[$1] = $2
            if ($3 + 0 > 0) hit[$1] = 1
        }
        END {
            for (b in stmts) {
                pkg = substr(b, 1, index(b, ":") - 1)
                sub(/\/[^\/]*$/, "", pkg)
                # Literal prefix strip: modp contains regex metacharacters (dots).
                pkg = pkg == modp ? "." : substr(pkg, length(modp) + 2)
                total[pkg] += stmts[b]
                if (b in hit) covered[pkg] += stmts[b]
            }
            for (p in total) printf "| `%s` | %.1f%% | %d |\n", p, covered[p] / total[p] * 100, total[p]
        }' "${PROFILE}" | sort
	echo ""
	echo "</details>"
} >"${OUT}"

echo "coverage-report: ${TOTAL} total, written to ${OUT}"
