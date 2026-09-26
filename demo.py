"""Runnable sample: bitemporal rectangle index end to end.

Run:  python3 demo.py
"""

from datetime import datetime, timezone

from bitemporal import (
    BitemporalStore,
    ChangePriority,
    Duration,
    Retract,
    SyntheticOffsetTable,
    TimePoint,
    Upsert,
    WallClockTime,
    verify_store,
)


def tp(seconds):
    return TimePoint.at(seconds=seconds)


def show(label, value):
    print(f"  {label:<46} {value}")


def main():
    print("=" * 78)
    print("1. Exact time: wall clock vs time point vs duration")
    print("=" * 78)
    table = SyntheticOffsetTable()
    table.add_anchor(
        tp(0),
        WallClockTime.from_datetime(datetime(2024, 1, 1, tzinfo=timezone.utc)),
    )
    point = tp(3600 * 25 + 60)  # synthetic t = 25h01m
    wall = table.to_wall_clock(point)
    show("synthetic TimePoint t=25h01m ->", wall.to_datetime().isoformat())
    show("round-trip back to TimePoint ->", table.to_time_point(wall) == point)
    show("t2 - t1 is a Duration ->", (tp(90) - tp(30)).total_seconds())
    try:
        point + wall  # noqa: B018
    except TypeError as exc:
        show("TimePoint + WallClockTime ->", f"TypeError ({exc.__class__.__name__})")

    print()
    print("=" * 78)
    print("2. Versioned corrections over a price record (EUR/USD, synthetic ticks)")
    print("=" * 78)
    store = BitemporalStore()
    v1 = store.upsert("px/A", tp(0), tp(10), 100, priority=0)
    v2 = store.upsert("px/B", tp(4), None, 200, priority=1)   # open ended
    v3 = store.upsert("px/A", tp(2), tp(8), 150, priority=0)  # retroactive fix
    v4 = store.transact([ChangePriority("px/A", 5)])          # atomic prio change
    v5 = store.transact([Retract("px/B")])                    # retraction
    print(f"  versions: insert A=v{v1}, insert B=v{v2}, fix A=v{v3}, "
          f"prio A=v{v4}, retract B=v{v5}")

    print()
    print("  value_at(version, t) -- later corrections never rewrite earlier")
    print("  observation versions:")
    header = "  version |" + "|".join(f" t={t:<4}" for t in (1, 3, 5, 7, 9, 12))
    print(header)
    for v in range(0, 6):
        cells = []
        for t in (1, 3, 5, 7, 9, 12):
            frag = store.value_at(v, tp(t))
            cells.append(f"{frag.record_id[-1]}={frag.value:<4}" if frag else "-    ")
        print(f"  v{v:<7} |" + "|".join(cells))

    print()
    print("=" * 78)
    print("3. Rectangle scan over a version x valid-time window")
    print("=" * 78)
    for f in store.scan(version_from=1, version_to=4,
                        valid_from=tp(3), valid_to=tp(9)):
        lo = "-inf" if f.valid_from is None else f.valid_from.micros // 1_000_000
        hi = "+inf" if f.valid_to is None else f.valid_to.micros // 1_000_000
        vto = "now" if f.version_to is None else f.version_to
        print(f"  {f.record_id:<6} value={f.value:<4} prio={f.priority} "
              f"valid=[{lo},{hi}) recorded=v{f.version_from}..{vto}")

    print()
    print("=" * 78)
    print("4. Provenance: why is this value visible at v2, t=5?")
    print("=" * 78)
    exp = store.explain(2, tp(5))
    show("winner ->", f"{exp.fragment.record_id} value={exp.fragment.value}")
    for c in exp.contenders:
        show(f"contender {c.record_id} (prio {c.priority}) ->", c.reason)

    print()
    print("=" * 78)
    print("5. Retraction closes rectangles but keeps history")
    print("=" * 78)
    for rect in store.history_of("px/B"):
        vto = "open" if rect.version_to is None else rect.version_to
        hi = "+inf" if rect.valid_to is None else rect.valid_to.micros // 1_000_000
        print(f"  px/B recorded v{rect.version_from}..{vto}: "
              f"valid=[{rect.valid_from.micros // 1_000_000},{hi}) value={rect.value}")

    print()
    print("=" * 78)
    print("6. Independent verification (naive replay vs index)")
    print("=" * 78)
    report = verify_store(store)
    show("versions checked ->", report.versions_checked)
    show("sample points checked ->", report.points_checked)
    show("rectangles structurally checked ->", report.rectangles_checked)
    show("result ->", "OK" if report.ok else report.errors)

    print()
    print("=" * 78)
    print("7. Query cost: probes vs history size")
    print("=" * 78)
    deep = BitemporalStore()
    for i in range(300):
        deep.upsert("hot", tp(0), tp(10), i)
    deep.upsert("cold", tp(1000), tp(1010), "cold")
    deep.reset_probes()
    deep.value_at(deep.version, tp(1005))
    show("rectangles in index ->", deep.index_stats()["rectangles"])
    show("probes for one point query ->", deep.probes)


if __name__ == "__main__":
    main()
