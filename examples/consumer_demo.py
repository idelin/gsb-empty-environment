"""Patch-consumer example.

Run with:  python -m examples.consumer_demo   (from the repository root)

A "device" owns a HistoryStore (direct query path). A downstream consumer is
handed only the initial segments and the patch stream, and reconstructs the
same segments with SegmentedView (patch-replay path).
"""

from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "src"))

from rlh import HistoryStore, Record, SegmentedView


def show(label: str, store: HistoryStore, view: SegmentedView) -> None:
    def render(segments):
        parts = []
        for seg in segments:
            refs = ",".join(f"{rec.source}@{rec.time}#s{rec.seq}" for rec in seg.sources)
            end = "inf" if seg.end is None else seg.end
            parts.append(f"  [{seg.start},{end}) {seg.state} id={seg.sid} <- {refs}")
        return "\n".join(parts) or "  <empty stream>"

    print(f"== {label} ==")
    print("direct query:")
    print(render(store.segments()))
    print("replayed from initial + patches:")
    print(render(view.segments()))
    assert [s.key for s in store.segments()] == [s.key for s in view.segments()]
    print("(both paths agree)\n")


def main() -> None:
    store = HistoryStore(
        [
            Record(time=0, state="A", source="gw1", seq=1),
            Record(time=2, state="A", source="gw1", seq=2),
            Record(time=5, state="B", source="gw1", seq=3),
        ]
    )
    view = SegmentedView(store.segments())
    show("initial A@0,A@2,B@5 -> two segments", store, view)

    # Insert B@1 between the A events: the A run splits, evidence is preserved.
    p = store.insert(Record(time=1, state="B", source="gw2", seq=10))
    print(f"insert B@1 -> removed {[s.sid for s in p.removed]}, "
          f"added {[s.sid for s in p.added]}")
    view.apply(p)
    show("after B@1 (A split into [0,1) and [2,5))", store, view)

    # Provenance after the split: each A half traces its own original event,
    # so every raw record remains reachable.
    assert [r.ref for r in store.trace_at(0)] == [("gw1", 1)]
    assert [r.ref for r in store.trace_at(2)] == [("gw1", 2)]
    print("split provenance: [0,1) <- gw1#1, [2,5) <- gw1#2")

    # Retract B@1: original two-segment structure and ids are restored.
    before_ids = [1, 2]
    p = store.retract("gw2", 10)
    print(f"\nretract B@1 -> removed {[s.sid for s in p.removed]}, "
          f"added {[s.sid for s in p.added]}")
    view.apply(p)
    show("after retracting B@1 (restored)", store, view)
    assert [s.sid for s in store.segments()] == before_ids
    print("segment ids restored:", [s.sid for s in store.segments()], "\n")

    # Same-time conflict: newer seq wins; the loser stays inspectable.
    p = store.insert(Record(time=0, state="C", source="gw3", seq=99))
    view.apply(p)
    conflict = store.conflict_at(0)
    print(f"conflict at t=0: winner {conflict.winner.ref} state={conflict.winner.state}, "
          f"shadowed {[(r.ref, r.state) for r in conflict.losers]}")
    p = store.retract("gw3", 99)
    view.apply(p)

    # Correction: move B@5 to B@6 via one atomic patch.
    p = store.correct(Record(time=6, state="B", source="gw1", seq=3))
    view.apply(p)
    show("after correcting gw1#3 from B@5 to B@6", store, view)


if __name__ == "__main__":
    main()
