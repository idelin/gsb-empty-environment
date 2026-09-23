"""Demo: replay a local change fixture and print per-group matrix folds.

Usage: python3 main.py [fixture.json]
"""

from __future__ import annotations

import sys

from matrix import fmt
from replay import load_fixture, replay


def main(argv) -> int:
    path = argv[1] if len(argv) > 1 else "fixtures/sample_changes.json"
    modulus, events = load_fixture(path)
    print(f"fixture: {path}")
    print(f"modulus: {modulus}")
    print(f"events : {len(events)} (applied in commit_seq order)")
    print("-" * 72)

    store, history = replay(modulus, events, verify=True)
    for event, products in history:
        print(f"seq={event.commit_seq:<3} {event.tx_id} {event.kind():<8} "
              f"table={event.table} pk={event.pk}")
        for group, product in products.items():
            print(f"    {group}: {fmt(product)}")

    print("-" * 72)
    ok = True
    for group in store.groups():
        incremental = store.product(group)
        scanned = store.full_scan_product(group)
        match = incremental == scanned
        ok = ok and match
        print(f"final {group}: incremental={fmt(incremental)} "
              f"full_scan={fmt(scanned)} match={match}")
    print("verification: " + ("OK (incremental == full scan for every group)"
                              if ok else "FAILED"))
    return 0 if ok else 1


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
