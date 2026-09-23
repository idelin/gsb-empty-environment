"""Demo: run every fixture scenario through the transaction buffer.

Usage: python3.11 main.py
"""
from __future__ import annotations

import os
import tempfile

from txbuffer import TransactionBuffer, feed, load_fixture

FIXTURES = os.path.join(os.path.dirname(os.path.abspath(__file__)), "fixtures")


def show(buffer: TransactionBuffer, label: str) -> None:
    st = buffer.status()
    reason = f" ({st['reason']})" if st["reason"] else ""
    print(
        f"    [{label}] state={st['state']}{reason} "
        f"last_seq={st['last_seq']} buffered={st['buffered_changes']} "
        f"users={buffer.read_all('users')}"
    )


def scenario(name: str, fixture: str, db_path: str, pause_after: int | None = None):
    print(f"== {name} ({fixture}) ==")
    events = load_fixture(os.path.join(FIXTURES, fixture))
    buffer = TransactionBuffer.open(db_path)
    for i, event in enumerate(events, 1):
        result = buffer.ingest(event)
        print(f"    ingest seq={event['seq']:>2} kind={event['kind']:<8} -> {result}")
        show(buffer, "after ingest")
        if pause_after is not None and i == pause_after:
            buffer.close()
            print("    -- restart (simulated crash / recovery) --")
            buffer = TransactionBuffer.open(db_path)
            show(buffer, "after restart")
    buffer.close()
    print()


def main() -> None:
    tmp = tempfile.mkdtemp(prefix="txbuffer-demo-")
    print(f"demo databases live under {tmp}\n")

    scenario("1. atomic commit", "01_atomic_commit.json", os.path.join(tmp, "s1.db"))
    scenario("2. rollback discards", "02_rollback.json", os.path.join(tmp, "s2.db"))
    scenario("3. sequence gap pauses", "03_gap_pause.json", os.path.join(tmp, "s3.db"))
    scenario("4. duplicate transaction", "04_duplicate_tx.json", os.path.join(tmp, "s4.db"))
    scenario("5. early commit marker waits", "05_early_commit.json", os.path.join(tmp, "s5.db"))
    scenario("6. delete-then-insert same key", "06_delete_then_insert.json", os.path.join(tmp, "s6.db"))
    scenario(
        "7. half transaction invisible after recovery",
        "07_half_tx_recovery.json",
        os.path.join(tmp, "s7.db"),
        pause_after=1,
    )


if __name__ == "__main__":
    main()
