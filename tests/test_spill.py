"""Spill buffer quotas, backpressure without row loss, temp-file lifecycle."""
from collections import Counter

from dynjoin import (BuildBarrier, BuildData, Deliver, Harness, JoinType,
                     NullMode, Poll, Rebuild, SpillBuffer, Strategy, Submit)


def test_memory_then_disk_then_backpressure(tmp_path):
    buf = SpillBuffer(str(tmp_path), mem_quota_rows=2, disk_quota_bytes=400)
    added = 0
    assert buf.try_add(0, ("b0", "r1")) is not None   # mem 1
    assert buf.try_add(0, ("b0", "r2")) is not None   # mem 2
    assert buf.try_add(0, ("b0", "r3")) is not None   # spills to disk
    added += 3
    assert buf.files(), "expected a temp spill file"
    while buf.try_add(0, ("b0", "x" * 100)) is not None:  # fill the disk quota
        added += 1
    assert buf.try_add(0, ("b0", "overflow")) is None  # both quotas full
    drained = buf.drain(0)
    assert len(drained) == added  # nothing lost
    assert buf.mem_count == 0 and buf.disk_used == 0
    assert buf.files() == []  # temp file released once drained


def test_rollback_on_failed_batch_staging(tmp_path):
    buf = SpillBuffer(str(tmp_path), mem_quota_rows=1, disk_quota_bytes=10)
    tokens = []
    tok = buf.try_add(0, ("b0", "r1"))
    assert tok is not None
    tokens.append(tok)
    assert buf.try_add(0, ("b0", "r2" + "x" * 100)) is None  # disk full
    buf.rollback(tokens)
    assert buf.mem_count == 0 and buf.disk_used == 0


def test_engine_spill_backpressure_no_row_loss(tmp_path):
    harness = Harness(
        {"num_partitions": 1, "join_type": JoinType.LEFT,
         "null_mode": NullMode.REJECT, "strategy": Strategy.SPILL,
         "mem_quota_rows": 1, "disk_quota_bytes": 200},
        tmpdir=str(tmp_path / "eng"))
    engine = harness.engine
    # filter incomplete: every row must be buffered (1 mem + 1 disk)
    assert engine.submit_scan_batch("s0", [("a", "s1"), ("b", "s2")])
    assert engine.spill.mem_count == 1 and engine.spill.disk_used > 0
    assert engine.spill.files(), "expected rows on temp disk"
    # a big batch that fits nowhere -> rejected whole, nothing staged
    big = [(f"k{i}", f"v{i}") for i in range(50)]
    assert not engine.submit_scan_batch("s1", big)
    assert engine.stats["backpressure"] == 1
    assert engine.spill.mem_count == 1  # rollback left the buffer untouched
    # barrier drains the buffer; the deferred batch then fits and is retried
    harness.run([
        Deliver(BuildData(0, 1, 1, "a", "b1")),
        Deliver(BuildBarrier(0, 1, 1)),
        Submit("s1", big),
        Poll(),
    ])
    expected = Counter([("a", "s1", "a", "b1"), ("b", "s2", None, None)]
                       + [(f"k{i}", f"v{i}", None, None) for i in range(50)])
    assert harness.consumer.multiset() == expected
    assert engine.spill.files() == []          # temp files released
    assert engine.spill.mem_count == 0 and engine.spill.disk_used == 0
    assert not engine.batches


def test_spill_rows_wait_for_their_own_epoch(tmp_path):
    harness = Harness(
        {"num_partitions": 1, "strategy": Strategy.SPILL,
         "mem_quota_rows": 10, "disk_quota_bytes": 10_000},
        tmpdir=str(tmp_path / "eng"))
    harness.run([
        Submit("s0", [("a", "s1")]),            # binds epoch 1, spills
        Rebuild(0, 2),
        Deliver(BuildData(0, 2, 1, "a", "n1")),
        Deliver(BuildBarrier(0, 2, 1)),         # completes epoch 2 only
        Submit("s1", [("a", "s2")]),            # binds epoch 2
        Poll(),
        Deliver(BuildData(0, 1, 1, "a", "o1")),
        Deliver(BuildBarrier(0, 1, 1)),         # now epoch 1 drains s0's row
        Poll(),
    ])
    assert harness.consumer.multiset() == Counter([
        ("a", "s1", "a", "o1"), ("a", "s2", "a", "n1"),
    ])
    assert harness.engine.spill.mem_count == 0
