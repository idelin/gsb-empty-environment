"""End-to-end demo of the vtline package.

Run with:  python3.11 demo.py
"""

from vtline import (
    Duration,
    Entry,
    Instant,
    OffsetTable,
    Repository,
    Segment,
    Timeline,
    WallClock,
    compose,
    diff,
    endpoints_of,
    full_scan_equal,
    inverse,
    merge_three_way,
    rebase,
)

# --- synthetic offset conversion table -------------------------------------
# Zone "SYN": wall = reference + 8h, with a leap-like +1s jump at JUMP.
BASE_OFFSET = Duration.hours(8)
JUMP = Instant(1_000_000_000_000_000)
TABLE = OffsetTable(
    [
        (Instant.EPOCH, BASE_OFFSET),
        (JUMP, BASE_OFFSET + Duration.seconds(1)),
    ]
)


def at(hour, minute=0, second=0):
    """Wall-clock reading on 2026-09-26 in zone SYN, converted to an Instant."""
    return TABLE.to_instant(WallClock(2026, 9, 26, hour, minute, second))


def show(instant):
    w = TABLE.to_wall(instant)
    return f"{w.hour:02d}:{w.minute:02d}:{w.second:02d}"


def show_span(c):
    before = c.before.value if c.before else "-"
    after = c.after.value if c.after else "-"
    return f"[{show(c.start)},{show(c.end)}) {c.kind.value:6s} {before!r} -> {after!r}"


def main():
    print("== 1. versioned valid-time timeline: add / correct / delete ==")
    repo = Repository()
    sensor_a_v1 = Entry("sensor-a", 20.5)
    sensor_b_v1 = Entry("sensor-b", 1013)

    t1 = Timeline([Segment(at(8), at(12), sensor_a_v1),
                   Segment(at(14), at(18), sensor_b_v1)])
    d1 = diff(repo.head, t1)
    s1 = repo.commit(d1)
    for c in d1.changes:
        print("  commit#1", show_span(c))
    print(f"  commit#1 nodes: copied={s1.copied_nodes} shared={s1.shared_nodes}")

    sensor_a_v2 = Entry("sensor-a", 21.0)  # correction, same identity
    t2 = Timeline([Segment(at(8), at(10), sensor_a_v2),
                   Segment(at(10), at(12), sensor_a_v1),
                   Segment(at(14), at(16), sensor_b_v1)])
    d2 = diff(repo.head, t2)
    s2 = repo.commit(d2)
    for c in d2.changes:
        print("  commit#2", show_span(c))
    print(f"  commit#2 nodes: copied={s2.copied_nodes} shared={s2.shared_nodes}")

    print()
    print("== 2. reversible apply + baseline summary verification ==")
    restored, s = repo.head.apply(inverse(d2))
    pts = endpoints_of(t1, t2, d2)
    print("  inverse(d2) restores commit#1:", full_scan_equal(restored, t1, pts))
    print(f"  baseline digest verified on apply: {t1.summary().digest[:16]}...")
    print(f"  undo nodes: copied={s.copied_nodes} shared={s.shared_nodes}")

    print()
    print("== 3. composition verified by full scan over original endpoints ==")
    combined = compose(d1, d2)
    direct, _ = Timeline().apply(combined)
    sequential = repo.head
    pts = endpoints_of(direct, sequential, d1, d2, combined)
    print("  apply(compose(d1,d2)) == apply(d1)->apply(d2):",
          full_scan_equal(direct, sequential, pts))
    print(f"  composed spans: {len(combined.changes)} "
          f"(vs {len(d1.changes)}+{len(d2.changes)} sequential)")

    print()
    print("== 4. three-way merge of two independent branch diffs ==")
    base = repo.versions[1]
    left_tl = Timeline([Segment(at(8), at(11), Entry("sensor-a", 22.5)),
                        Segment(at(11), at(12), sensor_a_v1),
                        Segment(at(14), at(18), sensor_b_v1)])
    right_tl = Timeline([Segment(at(8), at(10), sensor_a_v1),
                         Segment(at(10), at(12), Entry("sensor-a", 23.0)),
                         Segment(at(12), at(13), Entry("sensor-c", 7)),
                         Segment(at(14), at(18), Entry("sensor-b", 1012))])
    left = diff(base, left_tl)
    right = diff(base, right_tl)
    merged = merge_three_way(base, left, right)
    print(f"  conflicts: {len(merged.conflicts)}")
    for c in merged.conflicts:
        print(f"  conflict [{show(c.start)},{show(c.end)}) "
              f"base={c.base.value!r} left={c.left.value!r} right={c.right.value!r}")
    replay, _ = base.apply(merged.diff)
    pts = endpoints_of(base, merged.timeline, left, right, merged.diff)
    print("  merged diff replays merged timeline:",
          full_scan_equal(replay, merged.timeline, pts))

    print()
    print("== 5. rebase a branch diff onto an independent new base ==")
    moving = diff(base, left_tl)
    new_base = Timeline([Segment(at(8), at(12), sensor_a_v1),
                         Segment(at(14), at(18), sensor_b_v1),
                         Segment(at(20), at(22), Entry("sensor-d", 0))])
    rb = rebase(moving, base, new_base)
    rebased, _ = new_base.apply(rb.diff)
    pts = endpoints_of(base, new_base, rebased, moving, rb.diff)
    print(f"  conflicts during rebase: {len(rb.conflicts)}")
    print("  rebased diff applies cleanly to new base:",
          full_scan_equal(rebased, rb.timeline, pts))
    print("  sensor-d (unrelated) preserved:",
          rebased.entry_at(at(21)) == Entry("sensor-d", 0))

    print()
    print("== 6. retraction of a correction, by identity ==")
    repo.commit(diff(repo.head, Timeline([Segment(at(8), at(12), Entry("sensor-a", 99.9)),
                                          Segment(at(14), at(18), Entry("sensor-b", 99.9))])))
    print("  before retract: sensor-a =", repo.head.entry_at(at(9)).value,
          " sensor-b =", repo.head.entry_at(at(15)).value)
    repo.retract("sensor-a")
    print("  after retract:  sensor-a =", repo.head.entry_at(at(9)).value,
          " sensor-b =", repo.head.entry_at(at(15)).value,
          " (same value, different identity: untouched)")


if __name__ == "__main__":
    main()
