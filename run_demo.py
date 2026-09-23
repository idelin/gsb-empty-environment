"""Demo: feed local fixture shards into the assembler and print the outcome.

Usage: python3 run_demo.py
"""

import json
from pathlib import Path

from txn_assembler import (
    ChangeEvent,
    Manifest,
    TransactionAssembler,
    apply_to_store,
)

FIXTURE_DIR = Path(__file__).parent / "fixtures"
CLOCK_AFTER_TIMEOUT = 2500.0


def load_fixtures():
    manifests, event_stream = [], []
    for path in sorted(FIXTURE_DIR.glob("txn_*.json")):
        raw = json.loads(path.read_text(encoding="utf-8"))
        manifests.append(Manifest.from_dict(raw["manifest"]))
        event_stream.extend(ChangeEvent.from_dict(e) for e in raw["events"])
    return manifests, event_stream


def main() -> None:
    manifests, event_stream = load_fixtures()
    assembler = TransactionAssembler(manifests)

    print("== Receiving shards ==")
    for event in event_stream:
        state = assembler.receive(event)
        print(
            f"recv {event.txn_id} table={event.table} pk={event.primary_key} "
            f"seq={event.commit_seq} -> {state.value}"
        )

    print(f"\n== Timeout sweep at t={CLOCK_AFTER_TIMEOUT} ==")
    for txn_id in assembler.expire_timeouts(CLOCK_AFTER_TIMEOUT):
        print(f"timeout: {txn_id} marked PENDING_TIMEOUT (no partial commit)")

    print("\n== Final transaction states ==")
    for manifest in manifests:
        state = assembler.state_of(manifest.txn_id)
        reason = assembler.reject_reason_of(manifest.txn_id)
        suffix = f" ({reason.value})" if reason else ""
        print(f"{manifest.txn_id}: {state.value}{suffix}")

    store: dict[str, dict[str, object]] = {}
    for published_txn in assembler.published:
        apply_to_store(store, published_txn)

    print("\n== Published transactions (commit_seq order) ==")
    for published_txn in assembler.published:
        for event in published_txn:
            print(
                f"publish {event.txn_id} table={event.table} "
                f"pk={event.primary_key} seq={event.commit_seq} "
                f"{event.before} -> {event.after}"
            )

    print("\n== Downstream store ==")
    print(json.dumps(store, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
