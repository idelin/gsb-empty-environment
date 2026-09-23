"""Demo runner: apply a local change fixture and print group folds.

Usage: python3 main.py [fixtures/changes.json]

After every committed transaction the incrementally maintained segment-tree
product of each group is printed and verified against a full ordered scan.
"""

from __future__ import annotations

import json
import sys

from matrixfold import Change, FoldEngine, Transaction
from matrixfold.matrix import to_rows


def load_fixture(path: str) -> tuple[int, list[Transaction]]:
    """Load transactions (tx id, table, pk, before/after, commit seq)."""
    with open(path, "r", encoding="utf-8") as fh:
        doc = json.load(fh)
    transactions = [
        Transaction(
            tx_id=tx["tx_id"],
            commit_seq=int(tx["commit_seq"]),
            changes=tuple(
                Change(
                    table=ch["table"],
                    pk=str(ch["pk"]),
                    before=ch.get("before"),
                    after=ch.get("after"),
                )
                for ch in tx["changes"]
            ),
        )
        for tx in doc["transactions"]
    ]
    transactions.sort(key=lambda tx: (tx.commit_seq, tx.tx_id))
    return int(doc["modulus"]), transactions


def main(argv: list[str]) -> int:
    path = argv[1] if len(argv) > 1 else "fixtures/changes.json"
    modulus, transactions = load_fixture(path)
    engine = FoldEngine(modulus)
    print(f"fixture={path} modulus={modulus} transactions={len(transactions)}")
    for tx in transactions:
        engine.apply_transaction(tx)
        print(f"\n== tx {tx.tx_id} (commit_seq={tx.commit_seq}, "
              f"{len(tx.changes)} change(s)) ==")
        for name in engine.group_names():
            inc = engine.product(name)
            ref = engine.full_scan_product(name)
            status = "OK" if inc == ref else "MISMATCH"
            print(f"  group {name!r}: product={to_rows(inc)} [{status} vs full scan]")
            if inc != ref:
                return 1
    print("\nall incremental results match full-order scans")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
