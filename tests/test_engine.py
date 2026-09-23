"""Join semantics: inner/left, null modes, duplicate-key multiplicity,
the three incomplete-filter strategies, epoch isolation and stable IDs."""
from collections import Counter

import pytest

from dynjoin import (BuildBarrier, BuildData, Deliver, Harness, JoinType,
                     NullMode, Poll, Rebuild, Strategy, Submit)
from dynjoin.reference import nested_loop_join


def make(tmp_path, name, **overrides):
    kwargs = {"num_partitions": 1, "join_type": JoinType.INNER,
              "null_mode": NullMode.REJECT, "strategy": Strategy.WAIT}
    kwargs.update(overrides)
    return Harness(kwargs, tmpdir=str(tmp_path / name))


def test_inner_join_cartesian_multiplicity(tmp_path):
    h = make(tmp_path, "t")
    h.run([
        Deliver(BuildData(0, 1, 1, "a", "b1")),
        Deliver(BuildData(0, 1, 2, "a", "b2")),  # duplicate build key
        Deliver(BuildBarrier(0, 1, 2)),
        Submit("s0", [("a", "s1"), ("a", "s2")]),  # duplicate scan key
        Poll(),
    ])
    assert h.consumer.multiset() == Counter([
        ("a", "s1", "a", "b1"), ("a", "s1", "a", "b2"),
        ("a", "s2", "a", "b1"), ("a", "s2", "a", "b2"),
    ])


def test_left_join_complete_filter_exclusion_yields_null_row(tmp_path):
    h = make(tmp_path, "t", join_type=JoinType.LEFT)
    h.run([
        Deliver(BuildData(0, 1, 1, "a", "b1")),
        Deliver(BuildBarrier(0, 1, 1)),
        Submit("s0", [("a", "s1"), ("zzz", "s2")]),
        Poll(),
    ])
    # "zzz" is excluded by the complete filter -> null-padded row, not deleted
    assert h.consumer.multiset() == Counter([
        ("a", "s1", "a", "b1"),
        ("zzz", "s2", None, None),
    ])
    assert h.engine.stats["filter_negatives"] == 1


@pytest.mark.parametrize("strategy", list(Strategy))
def test_null_reject_mode(tmp_path, strategy):
    h = make(tmp_path, "t", join_type=JoinType.LEFT, strategy=strategy)
    h.run([
        Deliver(BuildData(0, 1, 1, None, "b1")),  # NULL build key
        Deliver(BuildBarrier(0, 1, 1)),
        Submit("s0", [(None, "s1"), ("a", "s2")]),
        Poll(),
    ])
    # NULL never matches in reject mode
    assert h.consumer.multiset() == Counter([
        (None, "s1", None, None),
        ("a", "s2", None, None),
    ])


@pytest.mark.parametrize("strategy", list(Strategy))
def test_null_safe_mode_matches_nulls(tmp_path, strategy):
    h = make(tmp_path, "t", join_type=JoinType.LEFT,
             null_mode=NullMode.SAFE, strategy=strategy)
    h.run([
        Deliver(BuildData(0, 1, 1, None, "b1")),
        Deliver(BuildBarrier(0, 1, 1)),
        Submit("s0", [(None, "s1"), ("a", "s2")]),
        Poll(),
    ])
    assert h.consumer.multiset() == Counter([
        (None, "s1", None, "b1"),   # NULL = NULL matches in safe mode
        ("a", "s2", None, None),
    ])


def test_wait_strategy_blocks_until_barrier(tmp_path):
    h = make(tmp_path, "t", strategy=Strategy.WAIT)
    h.run([
        Submit("s0", [("a", "s1"), ("zzz", "s2")]),  # filter incomplete
        Deliver(BuildData(0, 1, 1, "a", "b1")),
        Deliver(BuildBarrier(0, 1, 1)),
        Poll(),
    ])
    assert h.consumer.multiset() == Counter([("a", "s1", "a", "b1")])
    assert not h.engine.batches


def test_wait_backpressure_when_pending_queue_full(tmp_path):
    h = make(tmp_path, "t", strategy=Strategy.WAIT, max_pending_batches=1)
    assert h.engine.submit_scan_batch("s0", [("a", "s1")])
    # second batch would exceed the pending queue -> backpressure, no loss
    assert not h.engine.submit_scan_batch("s1", [("a", "s2")])
    h.run([
        Deliver(BuildData(0, 1, 1, "a", "b1")),
        Deliver(BuildBarrier(0, 1, 1)),
        Submit("s1", [("a", "s2")]),  # retry after drain
        Poll(),
    ])
    assert h.consumer.multiset() == Counter([
        ("a", "s1", "a", "b1"), ("a", "s2", "a", "b1"),
    ])


def test_bypass_emits_early_and_residual_after_barrier(tmp_path):
    h = make(tmp_path, "t", strategy=Strategy.BYPASS)
    h.run([
        Deliver(BuildData(0, 1, 1, "a", "b1")),
        Submit("s0", [("a", "s1"), ("c", "s2")]),  # partial probe, no filter
        Poll(),
        Deliver(BuildData(0, 1, 2, "a", "b2")),     # late duplicate key
        Deliver(BuildData(0, 1, 3, "c", "b3")),
        Deliver(BuildBarrier(0, 1, 3)),
        Poll(),
    ])
    # stable IDs: early emission then residual emission
    assert [bid for bid, _ in h.output_log] == ["s0#0", "s0#1"]
    assert h.consumer.multiset() == Counter([
        ("a", "s1", "a", "b1"), ("a", "s1", "a", "b2"), ("c", "s2", "c", "b3"),
    ])


def test_bypass_left_join_null_row_only_after_barrier(tmp_path):
    h = make(tmp_path, "t", strategy=Strategy.BYPASS, join_type=JoinType.LEFT)
    h.run([
        Submit("s0", [("c", "s1")]),  # nothing to match yet: no null row yet
        Poll(),
        Deliver(BuildData(0, 1, 1, "a", "b1")),
        Deliver(BuildBarrier(0, 1, 1)),
        Poll(),
    ])
    assert [bid for bid, _ in h.output_log] == ["s0#0"]
    assert h.consumer.multiset() == Counter([("c", "s1", None, None)])


def test_epoch_rebuild_isolation_and_release(tmp_path):
    h = make(tmp_path, "t")
    old_bp = h.engine.build.current[0]
    h.run([
        Submit("s0", [("a", "s1")]),          # binds epoch 1, waits
        Rebuild(0, 2),                        # version switch
        Deliver(BuildData(0, 2, 1, "a", "n1")),
        Deliver(BuildBarrier(0, 2, 1)),
        Submit("s1", [("a", "s2")]),          # binds epoch 2
        Poll(),
        # old epoch still completes for the running old batch
        Deliver(BuildData(0, 1, 1, "a", "o1")),
        Deliver(BuildBarrier(0, 1, 1)),
        Poll(),
    ])
    # s0 used the old version, s1 the new one: no mixing of filter/table
    assert h.consumer.multiset() == Counter([
        ("a", "s1", "a", "o1"), ("a", "s2", "a", "n1"),
    ])
    assert old_bp.released  # old epoch freed once its last user finished
    assert (0, 1) not in h.engine.build.by_version
    assert not h.engine.batches
