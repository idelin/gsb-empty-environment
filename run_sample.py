"""End-to-end demo: merge-aggregate 3 sorted streams with checkpoint/resume.

Run with:  python3.11 run_sample.py
"""

import math

from mergeagg import (
    NULL,
    AggSpec,
    Batch,
    Checkpoint,
    ColumnType,
    CursorVersionMismatchError,
    ListStream,
    MergeAggregator,
)

COLS = ("k", "v", "s")
TYPES = (ColumnType.STRING, ColumnType.INT, ColumnType.FLOAT)
SPECS = [
    AggSpec("v", "sum"),
    AggSpec("s", "sum"),
    AggSpec("s", "count"),
    AggSpec("s", "min"),
]
NAN = float("nan")


def batch(rows):
    return Batch(COLS, TYPES, rows)


def make_streams():
    return [
        ListStream("s1", [
            batch([("a", 10, 1.5), ("b", 20, NULL)]),
            batch([("b", 30, 2.5), ("d", 40, 3.5)]),
        ]),
        ListStream("s2", [
            batch([("a", 1, 0.5), ("b", 2, NAN)]),
            batch([("c", 3, 9.0)]),
        ]),
        ListStream("s3", [
            batch([("b", 100, 4.5), ("b", 200, NULL)]),
            batch([("e", 300, 6.5)]),
        ]),
    ]


def show(title, rows):
    print(f"--- {title} ---")
    for row in rows:
        cells = []
        for name, value in row.items():
            if isinstance(value, float) and math.isnan(value):
                value = "NaN"
            cells.append(f"{name}={value}")
        print("  " + ", ".join(cells))
    if not rows:
        print("  (none)")


def main():
    print("== Phase 1: run until pause (group 'b' spans all three streams) ==")
    merger = MergeAggregator(make_streams(), "k", SPECS, sleep_fn=lambda _: None)
    merger.run(pause_after_rows=4)  # stops mid-group 'b'
    show("committed before pause", merger.outputs)

    checkpoint = merger.checkpoint()
    payload = checkpoint.to_json()
    print(f"checkpoint json ({len(payload)} bytes):")
    print(f"  {payload}")
    print(f"pending group key: {checkpoint.pending.key!r}, "
          f"drained streams: {list(checkpoint.pending.drained)}")
    print(f"committed watermark: {checkpoint.committed_upto!r}")
    print(f"cursors: {[(c.stream_id, c.version, c.offset) for c in checkpoint.cursors.values()]}")

    print()
    print("== Phase 2: restore from the JSON checkpoint and finish ==")
    restored = MergeAggregator.restore(
        Checkpoint.from_json(payload),
        make_streams(),
        "k",
        SPECS,
        sleep_fn=lambda _: None,
    )
    restored.run()
    show("emitted after restore (no duplicates of committed groups)", restored.outputs)
    combined = merger.outputs + restored.outputs
    show("combined result", combined)

    print()
    print("== Reference: uninterrupted run ==")
    reference_merger = MergeAggregator(make_streams(), "k", SPECS, sleep_fn=lambda _: None)
    reference_merger.run()
    show("reference result", reference_merger.outputs)

    def norm(rows):
        out = []
        for row in rows:
            out.append({
                k: ("nan" if isinstance(v, float) and math.isnan(v)
                    else "null" if v is NULL else v)
                for k, v in row.items()
            })
        return out

    assert norm(combined) == norm(reference_merger.outputs), "MISMATCH!"
    print("pause/resume result == uninterrupted result: OK")

    print()
    print("== Cursor version mismatch demo ==")
    stale = [ListStream("s1", [], version=2), *make_streams()[1:]]
    try:
        MergeAggregator.restore(checkpoint, stale, "k", SPECS)
    except CursorVersionMismatchError as exc:
        print(f"raised as expected: {exc}")


if __name__ == "__main__":
    main()
