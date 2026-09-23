#!/bin/sh
# Regenerates the deterministic example corpus (train/ and val/).
set -e
cd "$(dirname "$0")"
gen() { # host hour count outfile seed
  h=$1; hour=$2; f=$4; s=$5
  : > "$f"
  n=1
  while [ $n -le 120 ]; do
    case $(( (n + s) % 4 )) in
      0) p=/api/users ;; 1) p=/api/orders ;; 2) p=/api/products ;; *) p=/healthz ;;
    esac
    code=200
    [ $(( (n * 7 + s) % 5 )) -eq 0 ] && code=500
    [ $(( (n * 3 + s) % 11 )) -eq 0 ] && code=404
    printf '2026-09-23T%s:%02d:%02dZ host=%s method=GET path=%s status=%d latency_ms=%d request_id=req-%05d cache=hit\n' \
      "$hour" $((n % 60)) $((n % 60)) "$h" "$p" "$code" $(( (n * 13) % 90 + 5 )) "$n" >> "$f"
    n=$((n + 1))
  done
}
gen web-01   10 120 train/node-1.log 1
gen web-02   10 120 train/node-2.log 2
gen cache-01 10 120 train/node-3.log 3
gen db-01    10 120 train/node-4.log 4
gen web-03   11 120 val/node-1.log 5
gen db-02    11 120 val/node-2.log 6
