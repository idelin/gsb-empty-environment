"""Regression: a checkpoint must be atomic with respect to the temp spill
files. Draining (and deleting) a spill file after a checkpoint must not lose
the checkpointed rows when a crash restores that checkpoint."""
from collections import Counter

from dynjoin import (BuildBarrier, BuildData, Checkpoint, Crash, Deliver,
                     Harness, JoinType, NullMode, Poll, Strategy, Submit)


def test_spill_file_delete_after_checkpoint_is_rolled_back(tmp_path):
    events = [
        # filter incomplete: both rows spill (mem quota 1 -> row 2 on disk)
        Submit("s0", [("a", "s1"), ("b", "s2")]),
        Checkpoint(),                       # captures mem row + disk file bytes
        Deliver(BuildData(0, 1, 1, "a", "b1")),
        Deliver(BuildBarrier(0, 1, 1)),     # drains and DELETES the spill file
        Poll(),
        Crash(),                            # restore must bring the rows back
        Deliver(BuildBarrier(0, 1, 1)),     # (replayed anyway by the harness)
        Poll(),
    ]
    harness = Harness(
        {"num_partitions": 1, "join_type": JoinType.LEFT,
         "null_mode": NullMode.REJECT, "strategy": Strategy.SPILL,
         "mem_quota_rows": 1, "disk_quota_bytes": 10_000},
        tmpdir=str(tmp_path / "t"))
    harness.run(events)
    # no row lost: the deduped multiset still matches the oracle
    assert harness.consumer.multiset() == Counter([
        ("a", "s1", "a", "b1"), ("b", "s2", None, None),
    ])
    assert not harness.engine.batches
    assert harness.engine.spill.files() == []
