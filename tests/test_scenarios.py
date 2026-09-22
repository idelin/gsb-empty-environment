"""Scenario tests: wrap vs reset, long gaps, backfill, tie-breaking."""

import pytest

from counter_analysis import (
    CounterConfig,
    IncrementalEngine,
    Reason,
    Sample,
    SourceConfig,
    recompute,
)

CFG = CounterConfig(
    sources={"dev": SourceConfig(bit_width=8, max_interval=10.0)},
    version="cfg-2026-09",
)


def s(sample_id, seq, t, count, reset=False, source="dev"):
    return Sample(sample_id, source, seq, t, count, reset)


def test_wrap_250_to_3_is_definite_plus_9():
    eng = IncrementalEngine(CFG)
    eng.insert(s("a", 1, 0.0, 250))
    rev = eng.insert(s("b", 2, 5.0, 3))
    (res,) = rev.added
    assert res.delta == 9  # 250 -> 255 -> 0 -> 3
    assert res.certain and res.reason is Reason.SINGLE_WRAP_BOUND
    assert eng.total("dev").definite_sum == 9
    assert eng.total("dev").complete


def test_same_numbers_with_reset_flag_mean_restart():
    eng = IncrementalEngine(CFG)
    eng.insert(s("a", 1, 0.0, 250))
    rev = eng.insert(s("b", 2, 5.0, 3, reset=True))
    (res,) = rev.added
    assert res.delta == 3  # not 9: counter restarted, 3 counted since reset
    assert res.reason is Reason.RESET
    assert eng.total("dev").definite_sum == 3


def test_long_gap_is_undecidable_and_total_not_complete():
    eng = IncrementalEngine(CFG)
    eng.insert(s("a", 1, 0.0, 250))
    rev = eng.insert(s("b", 2, 100.0, 3))  # gap 100 > max_interval 10
    (res,) = rev.added
    assert res.delta is None and not res.certain
    assert res.reason is Reason.GAP_UNDECIDABLE
    assert res.min_delta == 9  # lower bound: zero extra wraps
    total = eng.total("dev")
    assert not total.complete  # must not masquerade as a full total
    assert total.definite_sum == 0 and total.min_sum == 9
    assert total.uncertain == (("dev", "a", "b"),)


def test_backfill_conserves_and_revises_only_adjacent():
    eng = IncrementalEngine(CFG)
    eng.insert(s("a", 1, 0.0, 250))
    eng.insert(s("c", 3, 15.0, 3))  # gap 15 > 10: undecidable
    before = eng.total("dev")
    assert before.definite_sum == 0 and not before.complete

    # Late sample lands inside the undecidable gap, splitting it into two
    # decidable legs (gaps 5 and 10, both <= max_interval).
    rev = eng.insert(s("b", 2, 5.0, 200))
    assert [r.interval_id for r in rev.removed] == [("dev", "a", "c")]
    assert {r.interval_id for r in rev.added} == {("dev", "a", "b"), ("dev", "b", "c")}
    deltas = {r.interval_id: r.delta for r in rev.added}
    assert deltas[("dev", "a", "b")] == (200 - 250) % 256  # 206
    assert deltas[("dev", "b", "c")] == (3 - 200) % 256  # 59
    after = eng.total("dev")

    # Conservation: total changes by exactly the revised intervals.
    assert after.definite_sum == 265
    assert after.definite_sum - before.definite_sum == rev.definite_total_delta
    assert after.complete
    # ...and equals the from-scratch reference.
    ref = recompute(list(eng.samples("dev")), CFG)
    assert after == ref.totals["dev"]
    assert eng.intervals("dev") == {
        iid: res for iid, res in ref.intervals.items() if iid[0] == "dev"
    }


def test_backfill_with_one_still_long_leg_stays_incomplete():
    eng = IncrementalEngine(CFG)
    eng.insert(s("a", 1, 0.0, 250))
    eng.insert(s("c", 3, 100.0, 3))
    eng.insert(s("b", 2, 5.0, 200))  # b->c gap is 95 > 10: still undecidable
    total = eng.total("dev")
    assert total.definite_sum == 206  # only a->b is decidable
    assert not total.complete
    assert total.uncertain == (("dev", "b", "c"),)


def test_same_time_tie_broken_by_lower_seq_first():
    eng = IncrementalEngine(CFG)
    eng.insert(s("x", 2, 5.0, 100))  # inserted first, but seq 2
    rev = eng.insert(s("y", 1, 5.0, 90))  # same time, lower seq -> earlier
    (res,) = rev.added
    assert res.interval_id == ("dev", "y", "x")
    assert res.delta == 10


def test_retraction_revises_adjacent_intervals():
    eng = IncrementalEngine(CFG)
    for smp in [s("a", 1, 0.0, 10), s("b", 2, 5.0, 20), s("c", 3, 9.0, 35)]:
        eng.insert(smp)
    rev = eng.retract("b")
    assert {r.interval_id for r in rev.removed} == {("dev", "a", "b"), ("dev", "b", "c")}
    assert [r.interval_id for r in rev.added] == [("dev", "a", "c")]
    assert rev.added[0].delta == 25
    assert eng.total("dev").definite_sum == 25
    assert rev.definite_total_delta == 25 - (10 + 15)


def test_results_carry_sample_ids_and_config_version():
    eng = IncrementalEngine(CFG)
    eng.insert(s("a", 1, 0.0, 1))
    rev = eng.insert(s("b", 2, 1.0, 2))
    (res,) = rev.added
    assert (res.left_id, res.right_id) == ("a", "b")
    assert res.config_version == "cfg-2026-09"
    assert rev.config_version == "cfg-2026-09"


def test_out_of_range_count_rejected():
    eng = IncrementalEngine(CFG)
    with pytest.raises(ValueError):
        eng.insert(s("a", 1, 0.0, 256))
    with pytest.raises(ValueError):
        eng.insert(s("a", 1, 0.0, -1))
