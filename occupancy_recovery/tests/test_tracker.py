import random

import pytest

from occupancy import OccupancyTracker, Outcome, RecordStatus
from occupancy.brute_force import brute_force_peak


def period_keys(result):
    return [
        (p.start, p.end, p.level, p.evidence)
        for p in result.periods
    ]


def assert_matches_bruteforce(tracker, lo, hi):
    got = tracker.peak(lo, hi)
    ref = brute_force_peak(tracker.active_intervals(), lo, hi)
    assert got.peak == ref.peak
    assert period_keys(got) == period_keys(ref)
    return got


def test_half_open_boundary_does_not_overlap():
    tracker = OccupancyTracker()
    tracker.report_start("A", 0)
    tracker.report_end("A", 5)
    tracker.report_start("B", 5)
    tracker.report_end("B", 9)

    result = assert_matches_bruteforce(tracker, 0, 10)
    assert result.peak == 1
    # 交界处不重叠，但占用者从 A 换成 B，证据不同 -> 保留为两段。
    assert [(p.start, p.end, p.evidence) for p in result.periods] == [
        (0, 5, ("A",)),
        (5, 9, ("B",)),
    ]


def test_overlap_reaches_peak_two_and_withdraw_restores():
    tracker = OccupancyTracker()
    tracker.report_start("A", 0)
    tracker.report_end("A", 5)
    tracker.report_start("B", 5)
    tracker.report_end("B", 9)
    tracker.report_start("C", 4)
    tracker.report_end("C", 6)

    result = assert_matches_bruteforce(tracker, 0, 10)
    assert result.peak == 2
    peak_periods = [(p.start, p.end, p.evidence) for p in result.periods]
    assert peak_periods == [(4, 5, ("A", "C")), (5, 6, ("B", "C"))]

    assert tracker.withdraw("C").outcome is Outcome.WITHDRAWN
    restored = assert_matches_bruteforce(tracker, 0, 10)
    assert restored.peak == 1


def test_end_can_arrive_before_start():
    tracker = OccupancyTracker()
    first = tracker.report_end("X", 7)
    assert first.outcome is Outcome.RECORDED
    assert first.status is RecordStatus.END_ONLY
    assert tracker.orphan_records()[0].occupancy_id == "X"

    second = tracker.report_start("X", 3)
    assert second.outcome is Outcome.RECORDED
    assert second.status is RecordStatus.PAIRED
    assert second.interval == (3, 7)
    assert tracker.orphan_records() == []
    assert_matches_bruteforce(tracker, None, None)


def test_duplicate_end_is_explicit_and_state_unchanged():
    tracker = OccupancyTracker()
    tracker.report_start("A", 1)
    tracker.report_end("A", 4)
    duplicate = tracker.report_end("A", 4)
    assert duplicate.outcome is Outcome.DUPLICATE
    assert duplicate.status is RecordStatus.PAIRED
    assert duplicate.interval == (1, 4)
    assert tracker.active_intervals() == [(1, 4, "A")]


def test_endpoint_correction_replaces_old_contribution():
    tracker = OccupancyTracker()
    tracker.report_start("A", 0)
    tracker.report_end("A", 5)

    corrected = tracker.report_end("A", 8)
    assert corrected.outcome is Outcome.CORRECTED
    assert corrected.interval == (0, 8)
    assert tracker.active_intervals() == [(0, 8, "A")]
    assert_matches_bruteforce(tracker, 0, 10)


def test_correction_can_move_interval_out_of_query_range():
    tracker = OccupancyTracker()
    tracker.report_start("B", 5)
    tracker.report_end("B", 9)
    assert tracker.peak(0, 10).peak == 1

    # 合法地把 [5,9) 整体移到查询范围右侧 -> [10,12)
    assert tracker.report_end("B", 12).outcome is Outcome.CORRECTED
    assert tracker.report_start("B", 10).outcome is Outcome.CORRECTED

    inside = assert_matches_bruteforce(tracker, 0, 10)
    assert inside.peak == 0
    assert [(p.start, p.end, p.evidence) for p in inside.periods] == [(0, 10, ())]

    outside = assert_matches_bruteforce(tracker, 0, 12)
    assert outside.peak == 1
    assert [(p.start, p.end) for p in outside.periods] == [(10, 12)]


def test_invalid_end_before_start_does_not_change_state():
    tracker = OccupancyTracker()
    tracker.report_start("D", 8)
    bad = tracker.report_end("D", 3)
    assert bad.outcome is Outcome.REJECTED_INVALID
    record = tracker.record("D")
    assert record.status is RecordStatus.START_ONLY
    assert record.end is None
    assert tracker.active_intervals() == []


def test_invalid_start_against_preexisting_end_keeps_orphan():
    tracker = OccupancyTracker()
    tracker.report_end("E", 4)  # 先到的孤立 end
    bad = tracker.report_start("E", 9)  # 9 >= end 4
    assert bad.outcome is Outcome.REJECTED_INVALID
    record = tracker.record("E")
    assert record.status is RecordStatus.END_ONLY
    assert record.start is None
    # 任何范围都不能因为孤立 end 出现负占用
    assert tracker.peak(None, None).peak == 0


def test_endpoint_equal_to_start_is_rejected():
    tracker = OccupancyTracker()
    tracker.report_start("A", 5)
    bad = tracker.report_end("A", 5)
    assert bad.outcome is Outcome.REJECTED_INVALID
    assert tracker.record("A").status is RecordStatus.START_ONLY


def test_withdraw_removes_active_interval_and_clears_orphan():
    tracker = OccupancyTracker()
    tracker.report_start("A", 0)
    tracker.report_end("A", 4)
    tracker.report_end("B", 6)  # 孤立 end

    assert tracker.withdraw("A").outcome is Outcome.WITHDRAWN
    assert tracker.active_intervals() == []
    assert tracker.withdraw("B").outcome is Outcome.WITHDRAWN
    assert tracker.orphan_records() == []
    assert tracker.withdraw("A").outcome is Outcome.DUPLICATE
    assert tracker.withdraw("GHOST").outcome is Outcome.NOT_FOUND
    assert tracker.report_start("A", 1).outcome is Outcome.REJECTED_WITHDRAWN


def test_evidence_traces_back_to_contributing_ids():
    tracker = OccupancyTracker()
    tracker.report_end("A", 8)
    tracker.report_start("A", 0)
    tracker.report_start("B", 2)
    tracker.report_end("B", 5)
    tracker.report_start("C", 3)
    tracker.report_end("C", 9)

    result = assert_matches_bruteforce(tracker, 0, 10)
    assert result.peak == 3
    assert len(result.periods) == 1
    period = result.periods[0]
    assert (period.start, period.end) == (3, 5)
    assert set(period.evidence) == {"A", "B", "C"}


def test_unbounded_queries_are_supported():
    tracker = OccupancyTracker()
    tracker.report_start("A", -3)
    tracker.report_end("A", 2)
    assert_matches_bruteforce(tracker, None, None)
    assert_matches_bruteforce(tracker, None, 0)
    assert_matches_bruteforce(tracker, -10, None)


def test_interval_covering_entire_window_is_peak():
    tracker = OccupancyTracker()
    tracker.report_start("A", 0)
    tracker.report_end("A", 7)

    result = assert_matches_bruteforce(tracker, 2, 5)
    assert result.peak == 1
    assert [(p.start, p.end, p.evidence) for p in result.periods] == [
        (2, 5, ("A",))
    ]


def test_interval_ending_on_left_boundary_is_not_active():
    tracker = OccupancyTracker()
    tracker.report_start("A", 5)
    tracker.report_end("A", 8)

    result = assert_matches_bruteforce(tracker, 8, 10)
    assert result.peak == 0
    assert [(p.start, p.end, p.evidence) for p in result.periods] == [(8, 10, ())]


def test_invalid_query_range_rejected():
    tracker = OccupancyTracker()
    with pytest.raises(ValueError):
        tracker.peak(5, 5)


# ---- 随机差分：增量结果必须始终等于逐端点扫描 ---------------------------


def test_randomized_matches_brute_force():
    rng = random.Random(20260921)
    tracker = OccupancyTracker()

    for _ in range(3000):
        oid = f"id{rng.randrange(0, 40)}"
        action = rng.random()

        if action < 0.40:
            t = rng.randrange(-5, 16)
            result = tracker.report_start(oid, t)
        elif action < 0.80:
            t = rng.randrange(-5, 16)
            result = tracker.report_end(oid, t)
        else:
            result = tracker.withdraw(oid)

        # 撤回 / 非法拒绝后索引状态必须与记录状态严格自洽。
        active = {
            (s, e, i)
            for (s, e, i) in tracker.active_intervals()
        }
        expected = {
            (r.start, r.end, r.occupancy_id)
            for r in tracker._records.values()
            if r.is_active
        }
        assert active == expected

        lo = rng.choice([None, -6, 0, 2, 8])
        hi = rng.choice([None, 4, 10, 20])
        if lo is not None and hi is not None and hi <= lo:
            continue
        assert_matches_bruteforce(tracker, lo, hi)
