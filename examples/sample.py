"""End-to-end sample: merge, contradiction, revision, verification."""

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from logmerge import (
    Contradiction,
    Duration,
    Event,
    LogStore,
    Merger,
    OffsetTable,
    WallClock,
    verify_necessity,
)


def show_result(title, result):
    print(f"== {title} ==")
    if isinstance(result, Contradiction):
        print(f"  CONTRADICTION ({result.reason}): {result.message}")
        return
    for i, m in enumerate(result.order):
        mark = {True: "necessary", False: "UNCERTAIN", None: "-"}[m.next_necessary]
        print(
            f"  {i}: {m.event.id:<4} src={m.event.source} "
            f"ref=[{m.interval.lo.value}, {m.interval.hi.value}] "
            f"payload={m.event.payload!r:<14} next={mark}"
        )
    print()


def main():
    offsets = OffsetTable({"A": 0, "B": 2, "C": "-1/2"})

    store = LogStore(offsets)
    store.add_event(Event("a1", "A", 0, WallClock("A", 100), Duration(1), "boot"))
    store.add_event(Event("a2", "A", 1, WallClock("A", 103), Duration(1), "ready"))
    store.add_event(
        Event("b1", "B", 0, WallClock("B", "98.5"), Duration("1/2"),
              "recv-from-a1", causal_after=("a1",))
    )
    store.add_event(Event("b2", "B", 1, WallClock("B", 103), Duration(1), "flush"))
    store.add_event(Event("c1", "C", 0, WallClock("C", 101), Duration(3), "tick"))

    v1 = store.publish()
    print("note: b1 wall-clock reading 98.5 < a1 reading 100, but explicit")
    print("causality b1 after a1 wins over the apparent wall-clock order.\n")
    show_result(f"version {v1.number} merged order", v1.result)

    problems = verify_necessity(
        list(v1.result.ids), list(v1.result.edges), v1.result.necessary
    )
    print(f"enumeration cross-check of necessary pairs: "
          f"{'OK' if not problems else problems}\n")

    print("-- correction: c1 clock 101 -> 105, error 3 -> 1/2 --")
    rev = store.revise(
        [Event("c1", "C", 0, WallClock("C", 105), Duration("1/2"), "tick")]
    )
    show_result(f"version {rev.new.number} merged order", rev.new.result)
    print(f"  delta moved:   {rev.delta.moved}")
    print(f"  delta added:   {rev.delta.added}")
    print(f"  delta removed: {rev.delta.removed}")
    print(f"  old readers still see version {rev.old.number}: "
          f"{rev.old.result.ids}\n")

    print("-- contradiction demos (timestamps are never modified) --")
    cyc = Merger(offsets).merge([
        Event("x", "A", 0, WallClock("A", 10), Duration(0), "x",
              causal_after=("y",)),
        Event("y", "A", 1, WallClock("A", 20), Duration(0), "y"),
    ])
    show_result("causal edge contradicts disjoint time intervals", cyc)

    inf = Merger(offsets).merge([
        Event("p", "A", 0, WallClock("A", 10), Duration(0), "p"),
        Event("q", "A", 1, WallClock("A", 10), Duration(0), "q",
              causal_after=("p",)),
    ])
    show_result("strict causal order over equal exact timestamps", inf)


if __name__ == "__main__":
    main()
