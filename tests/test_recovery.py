"""Checkpoint/restore, fault injection around output, stable-ID redelivery."""
from collections import Counter

import pytest

from dynjoin import (AckAll, BuildBarrier, BuildData, Checkpoint, Crash,
                     Deliver, Harness, JoinEngine, JoinType, NullMode, Poll,
                     Rebuild, Strategy, Submit)
from dynjoin.reference import nested_loop_join

SCAN_A = Submit("s0", [("a", "s1"), ("b", "s2"), (None, "s3")])
SCAN_B = Submit("s1", [("a", "s4"), ("c", "s5")])

CORE = [
    Deliver(BuildData(0, 1, 1, "a", "b1")),
    Deliver(BuildData(0, 1, 2, "b", "b2")),
    Deliver(BuildBarrier(0, 1, 2)),
    SCAN_A,
    Poll(),
    AckAll(),
    SCAN_B,
    Poll(),
    AckAll(),
]

EXPECTED = Counter(
    nested_loop_join(SCAN_A.rows + SCAN_B.rows,
                     [("a", "b1"), ("b", "b2")], JoinType.LEFT, NullMode.REJECT))


@pytest.mark.parametrize("strategy", list(Strategy))
def test_crash_at_every_position(tmp_path, strategy):
    """Checkpoint before every event, then inject a crash at each position
    (before/after outputs). The deduped result must always match the oracle."""
    for crash_at in range(len(CORE) + 1):
        events = []
        for idx, event in enumerate(CORE):
            events.append(Checkpoint())
            if idx == crash_at:
                events.append(Crash())
            events.append(event)
        if crash_at == len(CORE):
            events += [Checkpoint(), Crash()]
        harness = Harness(
            {"num_partitions": 1, "join_type": JoinType.LEFT,
             "null_mode": NullMode.REJECT, "strategy": strategy},
            tmpdir=str(tmp_path / f"c{crash_at}"))
        harness.run(events)
        assert harness.consumer.multiset() == EXPECTED
        assert not harness.engine.batches


def test_unacked_outputs_are_redelivered_and_deduped(tmp_path):
    events = [
        Deliver(BuildData(0, 1, 1, "a", "b1")),
        Deliver(BuildBarrier(0, 1, 1)),
        Checkpoint(),
        Submit("s0", [("a", "s1")]),
        Poll(),          # consumer sees the output...
        Crash(),         # ...but the ack is lost before the checkpoint
        Poll(),          # recovery redelivers the same stable ID
        AckAll(),
    ]
    harness = Harness({"num_partitions": 1}, tmpdir=str(tmp_path / "t"))
    harness.run(events)
    assert harness.consumer.duplicates == 1  # redelivery happened
    assert harness.consumer.multiset() == Counter([("a", "s1", "a", "b1")])


def test_checkpoint_carries_required_state(tmp_path):
    harness = Harness(
        {"num_partitions": 1, "join_type": JoinType.LEFT,
         "strategy": Strategy.WAIT},
        tmpdir=str(tmp_path / "t"))
    harness.run([
        Deliver(BuildData(0, 1, 1, "a", "b1")),   # applied, no barrier yet
        Submit("s0", [("a", "s1")]),              # pending scan batch
        Submit("s1", [("z", "s2")]),              # another pending batch
        Checkpoint(),
    ])
    engine = JoinEngine.restore(harness.last_ckpt)
    # input position
    assert engine.scan_position == 2
    # partition completeness
    bp = engine.build.current[0]
    assert bp.applied == {1} and bp.barrier is None and not bp.complete
    # pending scan batches
    assert set(engine.batches) == {"s0", "s1"}
    assert list(engine.wait_queue) == ["s0", "s1"]
    # unacked outputs (none yet)
    assert engine.unacked == {}
    # resume: barrier completes the partition, pending batches resolve
    harness.engine = engine
    harness.run([
        Deliver(BuildBarrier(0, 1, 1)),
        Poll(),
    ])
    assert harness.consumer.multiset() == Counter([
        ("a", "s1", "a", "b1"), ("z", "s2", None, None),
    ])


def test_unacked_outputs_survive_checkpoint(tmp_path):
    harness = Harness({"num_partitions": 1}, tmpdir=str(tmp_path / "t"))
    harness.run([
        Deliver(BuildData(0, 1, 1, "a", "b1")),
        Deliver(BuildBarrier(0, 1, 1)),
        Submit("s0", [("a", "s1")]),
        Checkpoint(),   # output emitted but unacked -> must be in checkpoint
        Crash(),
        Poll(),
    ])
    assert harness.consumer.multiset() == Counter([("a", "s1", "a", "b1")])


def test_rebuild_and_crash_replay_keeps_epochs_consistent(tmp_path):
    events = [
        Deliver(BuildData(0, 1, 1, "a", "o1")),
        Checkpoint(),
        Rebuild(0, 2),                       # version switch after checkpoint
        Deliver(BuildData(0, 2, 1, "a", "n1")),
        Deliver(BuildBarrier(0, 2, 1)),
        Deliver(BuildBarrier(0, 1, 1)),
        Submit("s0", [("a", "s1")]),         # binds epoch 2
        Poll(),
        Crash(),                             # replay must redo the switch
        AckAll(),
        Poll(),
    ]
    harness = Harness({"num_partitions": 1}, tmpdir=str(tmp_path / "t"))
    harness.run(events)
    assert harness.consumer.multiset() == Counter([("a", "s1", "a", "n1")])
    assert harness.engine.build.current[0].epoch == 2
