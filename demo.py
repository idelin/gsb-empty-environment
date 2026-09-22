"""Runnable end-to-end example for out-of-order occupancy reconstruction."""

from occupancy_recovery import OccupancyTracker


def show(title: str, tracker: OccupancyTracker, lo: int, hi: int) -> None:
    result = tracker.peak(lo, hi)
    print(f"\n{title}")
    print(f"peak={result.peak}")
    print("maximal peak periods:")
    for period in result.periods:
        print(f"  [{period.start}, {period.end})")
    print("evidence:")
    for segment in result.evidence_segments:
        print(
            f"  [{segment.start}, {segment.end}) "
            f"count={segment.occupancy} ids={segment.occupancy_ids}"
        )

    snapshot = tracker.snapshot()
    print("intervals:", snapshot.intervals)
    print("unmatched starts:", snapshot.unmatched_starts)
    print("unmatched ends:", snapshot.unmatched_ends)
    print("invalid:", snapshot.invalid_records)


def main() -> None:
    tracker = OccupancyTracker(seed=42)

    # An end can arrive before its matching start.
    tracker.submit_end("B", 9)
    tracker.submit_start("A", 0)
    tracker.submit_end("A", 5)
    tracker.submit_start("B", 5)
    show("A=[0,5), B=[5,9): touching intervals do not overlap", tracker, 0, 10)

    tracker.submit_start("C", 4)
    tracker.submit_end("C", 6)
    show("after adding C=[4,6): peak becomes 2", tracker, 0, 10)

    tracker.withdraw("C")
    show("after withdrawing C: previous contribution is fully removed", tracker, 0, 10)

    tracker.submit_start("D", 2)
    tracker.submit_end("D", 8)
    show("after adding D=[2,8)", tracker, 0, 10)

    tracker.replace_interval("D", 12, 18)
    show("after correcting D outside [0,10): old contribution is revoked", tracker, 0, 10)

    tracker.submit_end("E", 1)
    bad = tracker.submit_start("E", 3)
    assert bad.status == tracker.INVALID_RECORDED
    show("invalid E end-before-start is isolated and never creates occupancy", tracker, 0, 20)

    valid = tracker.submit_start("E", 0)
    assert valid.status == tracker.COMPLETED
    show("after correcting E to [0,1)", tracker, 0, 20)


if __name__ == "__main__":
    main()
