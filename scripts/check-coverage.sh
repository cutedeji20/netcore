#!/usr/bin/env bash
# §74 coverage floors.
#
# Measures TRUE statement coverage per package by summing the statement counts
# in the coverage profile. An earlier version averaged per-function percentages
# from `go tool cover -func`, which overstates a package whose uncovered
# functions are small — it reported internal/quota at 95% when real statement
# coverage was 86.4%. A coverage gate that flatters you is worse than none.
#
# Profile line format:  <path>.go:<l.c>,<l.c> <numStmts> <count>
set -euo pipefail

PROFILE="${1:-coverage.out}"
[[ -f "$PROFILE" ]] || { echo "no coverage profile at $PROFILE"; exit 1; }

# §74 floors. quota is the money path and carries the highest floor.
declare -A FLOOR=(
  ["internal/quota"]=85
  ["internal/subscriptions"]=85
  ["internal/config"]=75
  ["internal/security"]=75
  ["internal/logger"]=75
  ["pkg/money"]=85
)

# Emit "<package> <covered_stmts> <total_stmts>" per package.
mapfile -t rows < <(
  awk '
    NR == 1 { next }                        # skip "mode:" header
    {
      split($1, loc, ":")
      path = loc[1]
      n = split(path, parts, "/")
      pkg = parts[n-1]
      for (i = n-2; i >= 1; i--) {
        if (parts[i] == "internal" || parts[i] == "pkg") { pkg = parts[i] "/" pkg; break }
      }
      total[pkg] += $2
      if ($3 > 0) covered[pkg] += $2
    }
    END { for (p in total) printf "%s %d %d\n", p, covered[p], total[p] }
  ' "$PROFILE"
)

fail=0
for pkg in "${!FLOOR[@]}"; do
  line=$(printf '%s\n' "${rows[@]}" | awk -v p="$pkg" '$1 == p {print; exit}')
  if [[ -z "$line" ]]; then
    echo "FAIL ${pkg}: no coverage data (package not tested?)"
    fail=1
    continue
  fi

  covered=$(awk '{print $2}' <<<"$line")
  total=$(awk '{print $3}' <<<"$line")
  floor=${FLOOR[$pkg]}
  pct=$(( total > 0 ? covered * 100 / total : 0 ))

  if (( pct < floor )); then
    printf 'FAIL %-26s %3d%% < %d%% floor  (%d/%d statements) [§74]\n' \
      "$pkg" "$pct" "$floor" "$covered" "$total"
    fail=1
  else
    printf 'ok   %-26s %3d%% (floor %d%%)  (%d/%d statements)\n' \
      "$pkg" "$pct" "$floor" "$covered" "$total"
  fi
done

exit $fail
