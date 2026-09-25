"""Runnable synthetic sample covering every solver feature.

Run from the repository root:
    python3.11 examples/sample.py
"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from evidence import (  # noqa: E402
    EMPTY_INTERSECTION,
    FEASIBLE,
    FactVersion,
    IncrementalEngine,
    MutexRule,
    Query,
    common_interval,
    replay_solve,
    solve,
)


def show(title, result):
    print(f"\n== {title} ==")
    if not result.segments:
        print("  (no segments)")
    for seg in result.segments:
        print(f"  {seg.segment_id}")
        for sel in seg.witnesses:
            print(f"    witness: {list(sel.refs)} common={sel.common_interval()}")
        for reason in seg.reasons:
            print(f"    reason: {reason.code} - {reason.detail}")
        if seg.status == FEASIBLE:
            print(f"    single selection covers whole segment: {seg.covers_entire_segment}")
    print(f"  journal digest: {result.journal.digest()[:16]}... "
          f"({len(result.journal.steps)} steps)")


def base_facts():
    return [
        FactVersion("price-vendorA", 1, "price", "vendorA", 0, 10, recorded_at=1, value=41.2),
        FactVersion("price-vendorB", 1, "price", "vendorB", 4, 8, recorded_at=1, value=41.0),
        FactVersion("rating-manual", 1, "rating", "manual", 0, 6, recorded_at=2, value="B"),
        FactVersion("rating-manual", 2, "rating", "manual", 0, 9, recorded_at=7, value="A"),
        FactVersion("price-archive", 1, "price", "archive", 0, 9, recorded_at=100, value=40.9),
    ]


def main():
    rules = [MutexRule("mutex-vendors", 1, frozenset({"vendorA", "vendorB"}))]
    facts = base_facts()

    show("observation t=5 (rating v1 visible, archive excluded)",
         solve(facts, rules, Query("q-obs5", 5, ("price", "rating"))))

    show("observation t=9 (corrected rating v2 visible)",
         solve(facts, rules, Query("q-obs9", 9, ("price", "rating"))))

    show("observation t=1.5 (rating recorded later -> NOT_VISIBLE)",
         solve(facts, rules, Query("q-obs15", 1.5, ("price", "rating"))))

    print("\n== touching intervals [0,5) and [5,9) ==")
    left = FactVersion("m-left", 1, "price", "vendorA", 0, 5, recorded_at=1)
    right = FactVersion("m-right", 1, "rating", "manual", 5, 9, recorded_at=1)
    pair = (left, right)
    interval = common_interval(pair)
    print(f"  common interval of {left.ref} and {right.ref}: {interval}"
          f" -> {EMPTY_INTERSECTION if interval is None else 'ok'}")
    show("solver view of the touching pair",
         solve(pair, [], Query("q-touch", 10, ("price", "rating"))))

    alt_facts = [
        FactVersion("alt-a", 1, "price", "vendorA", 0, 5, recorded_at=1),
        FactVersion("alt-b", 1, "price", "vendorB", 5, 9, recorded_at=1),
        FactVersion("alt-m", 1, "rating", "manual", 0, 9, recorded_at=1),
    ]
    naive_lo = max(f.valid_from for f in alt_facts)
    naive_hi = min(f.valid_to for f in alt_facts)
    print("\n== not just the intersection of all candidates ==")
    print(f"  naive intersection of every candidate: [{naive_lo}, {naive_hi}) -> empty")
    show("solver keeps per-alternative witnesses",
         solve(alt_facts, rules, Query("q-alt", 10, ("price", "rating"))))

    print("\n== incremental update: price-vendorA corrected to [3,10) at t=8 ==")
    engine = IncrementalEngine(rules, Query("q-inc", 9, ("price", "rating")))
    show("before update (observation t=9)", engine.load(facts))
    report = engine.apply_update(
        FactVersion("price-vendorA", 2, "price", "vendorA", 3, 10, recorded_at=8, value=41.4)
    )
    print(f"  update {report.fact_ref}: changed={report.changed} "
          f"affected={report.affected_range}")
    print(f"  undone segments: {list(report.undone_segment_ids)}")
    print(f"  new segments in range: {list(report.new_segment_ids)}")
    show("after update", engine.result)
    print(f"  engine replay consistent: {engine.replay()}")

    print("\n== invisible update is a no-op ==")
    noop = engine.apply_update(
        FactVersion("price-vendorA", 3, "price", "vendorA", 0, 1, recorded_at=50)
    )
    print(f"  update {noop.fact_ref}: changed={noop.changed} (recorded after observation)")

    print("\n== budget-limited search keeps unknown segments ==")
    limited = solve(alt_facts, rules, Query("q-budget", 10, ("price", "rating")),
                    max_atomic_segments=1)
    show("max_atomic_segments=1", limited)

    print("\n== replay verification ==")
    result = solve(facts, rules, Query("q-obs9", 9, ("price", "rating")))
    ok = replay_solve(facts, rules, Query("q-obs9", 9, ("price", "rating")), result)
    print(f"  replay of q-obs9: {ok}")


if __name__ == "__main__":
    main()
