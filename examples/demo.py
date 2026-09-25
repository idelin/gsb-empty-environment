"""End-to-end demo: merge-aggregate three sorted streams with a
checkpoint taken while a group is still incomplete, then resume.

Run from the repository root:

    python3.11 examples/demo.py
"""

import os
import sys
import tempfile

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from merge_agg import (  # noqa: E402
    COMPLETE,
    PAUSED,
    Batch,
    CursorVersionMismatch,
    ListStream,
    MergeAggregator,
    Schema,
)

SCHEMA = Schema([("k", "int"), ("v", "int"), ("x", "float")])
AGGS = [("sum", "v"), ("count", "v")]

FULL_DATA = {
    "a": [[(1, 100, 0.0), (2, 1, 0.0), (3, 5, 0.0)]],
    "b": [[(1, 200, 0.0), (2, 10, 0.0), (2, 20, 0.0)]],
    "c": [[(1, 300, 0.0), (2, 100, 0.0), (4, 7, 0.0)]],
}


def make_stream(stream_id, rows_by_batch, version=1, closed=True):
    stream = ListStream(stream_id, SCHEMA, version=version)
    for rows in rows_by_batch:
        stream.append(Batch(SCHEMA, rows))
    if closed:
        stream.close()
    return stream


def show(title, sink):
    print(title)
    for key, results in sink:
        print(f"  key={key}  sum(v)={results[0]}  count(v)={results[1]}")


def main():
    print("=== Phase 1: partial input, group 2 spans three streams ===")
    streams = [
        make_stream("a", FULL_DATA["a"], closed=False),
        make_stream("b", [[(1, 200, 0.0), (2, 10, 0.0)]], closed=False),
        make_stream("c", FULL_DATA["c"], closed=False),
    ]
    merger = MergeAggregator(streams, "k", AGGS)
    sink = []
    status = merger.run(sink)
    print(f"status: {status}  (stream 'b' ran dry mid-group; "
          f"temporary emptiness is not EOF)")
    show("committed so far:", sink)

    tmpdir = tempfile.mkdtemp(prefix="merge_agg_demo_")
    path = os.path.join(tmpdir, "checkpoint.pkl")
    merger.save_checkpoint(path)
    state = merger.checkpoint()
    cursors = {c.stream_id: c.rows_consumed for c in state["cursors"]}
    print(f"checkpoint saved to {path}")
    print(f"  incomplete group key: {state['current_key']}, "
          f"partial sum: {state['current_accs'][0].result()}")
    print(f"  cursors (rows consumed): {cursors}")

    print()
    print("=== Phase 2: restore from checkpoint with the full streams ===")
    restored = MergeAggregator.restore(
        path, [make_stream(s, FULL_DATA[s]) for s in "abc"])
    status = restored.run(sink)
    print(f"status: {status}")
    show("final output (group 1 committed once, never re-emitted):", sink)

    print()
    print("=== Baseline: same data in one uninterrupted run ===")
    baseline = []
    MergeAggregator([make_stream(s, FULL_DATA[s]) for s in "abc"],
                    "k", AGGS).run(baseline)
    show("baseline output:", baseline)
    assert sink == baseline, "pause/resume output differs from baseline!"
    print("pause/resume output is identical to the uninterrupted run")

    print()
    print("=== Cursor version mismatch ===")
    stale = [make_stream(s, FULL_DATA[s], version=2) for s in "abc"]
    try:
        MergeAggregator.restore(path, stale)
    except CursorVersionMismatch as exc:
        print(f"raised CursorVersionMismatch: {exc}")


if __name__ == "__main__":
    main()
