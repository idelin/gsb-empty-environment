#!/usr/bin/env python3.11
"""End-to-end demo: generate -> validate -> separate error kinds -> shrink.

Run:  python3.11 run_demo.py
"""

from pathlib import Path

from cdcstream import (
    ArrivalOrderProcessor,
    IncrementalProcessor,
    StreamGenerator,
    format_stream,
    full_state,
    load_events,
    run_and_validate,
    shrink,
)

FIXTURE = Path(__file__).parent / "fixtures" / "changes.json"
SEED = 7


def main() -> None:
    events = load_events(FIXTURE)
    print(f"Loaded {len(events)} changes from {FIXTURE.name} "
          f"({len({e.tx_id for e in events})} transactions)")
    print(f"Oracle full state: {full_state(events)}")

    stream = StreamGenerator(SEED).generate(
        events, reorder_window=4, duplicate_probability=0.4, crashes=2
    )
    print(f"\nGenerated delivery stream (seed={SEED}): {len(stream)} items "
          f"({len(events)} logical events, duplicates + reorder + crashes)")

    print("\n=== 1. Correct incremental processor ===")
    result = run_and_validate(stream, IncrementalProcessor)
    print(f"status: {result.status}")

    print("\n=== 2. Buggy arrival-order processor (expect mismatch) ===")
    result = run_and_validate(stream, ArrivalOrderProcessor)
    print(f"status: {result.status} ({result.detail})")
    for key, (got, want) in sorted(result.differing.items()):
        print(f"  {key}: processor={got!r} oracle={want!r}")
    for key, value in sorted(result.unexpected.items()):
        print(f"  {key}: unexpected {value!r}")

    print("\n=== 3. Invalid stream is reported separately ===")
    bad = StreamGenerator(1).generate_invalid(events, "partial_tx")
    result = run_and_validate(bad, IncrementalProcessor)
    print(f"status: {result.status} ({result.detail})")

    print("\n=== 4. Automatic shrinking of the failing stream ===")
    shrunk = shrink(stream, ArrivalOrderProcessor)
    print(f"shrunk: {len(stream)} -> {len(shrunk)} items")
    print(format_stream(shrunk))
    recheck = run_and_validate(shrunk, ArrivalOrderProcessor)
    print(f"re-validation: {recheck.status} "
          f"(still failing, transaction structure replayable)")


if __name__ == "__main__":
    main()
