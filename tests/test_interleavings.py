"""Enumerate interleavings of barrier, last build message, version switch and
scan batch; plus a seeded fuzzer mixing in crashes. Every run is checked
against the independent filter-less nested-loop join."""
import itertools
import random
from collections import Counter

import pytest

from dynjoin import (NULL, AckAll, BuildBarrier, BuildData, Checkpoint,
                     Crash, Deliver, Harness, JoinType, NullMode, Poll,
                     Rebuild, Strategy, Submit)
from dynjoin.filters import default_hash
from dynjoin.reference import nested_loop_join

M1 = Deliver(BuildData(0, 1, 1, "a", "b1"))
M2 = Deliver(BuildData(0, 1, 2, "b", "b2"))   # last build message of epoch 1
BAR = Deliver(BuildBarrier(0, 1, 2))
SCAN = Submit("s0", [("a", "s1"), ("c", "s2"), (None, "s3")])
REB = Rebuild(0, 2)                            # version switch
TAIL = [Deliver(BuildData(0, 2, 1, "c", "b3")),
        Deliver(BuildBarrier(0, 2, 1))]


@pytest.mark.parametrize("strategy", list(Strategy))
@pytest.mark.parametrize("join_type", [JoinType.INNER, JoinType.LEFT])
def test_all_interleavings(tmp_path, strategy, join_type):
    for index, perm in enumerate(itertools.permutations([M1, M2, BAR, SCAN, REB])):
        events = list(perm) + TAIL + [Poll(), AckAll()]
        harness = Harness(
            {"num_partitions": 1, "join_type": join_type,
             "null_mode": NullMode.REJECT, "strategy": strategy},
            tmpdir=str(tmp_path / f"p{index}"))
        harness.run(events)
        assert not harness.engine.batches, f"unfinished batches in {perm}"
        # the scan batch binds whichever epoch was current when submitted
        bound_epoch1 = perm.index(SCAN) < perm.index(REB)
        build_rows = [("a", "b1"), ("b", "b2")] if bound_epoch1 else [("c", "b3")]
        expected = Counter(nested_loop_join(SCAN.rows, build_rows,
                                            join_type, NullMode.REJECT))
        assert harness.consumer.multiset() == expected, f"interleaving {perm}"


def generate_script(rng, num_partitions, num_batches, hash_fn):
    """Random but always-completable script: every epoch's messages and
    barrier are eventually delivered (shuffled, with redeliveries), rebuilds
    precede their epoch's messages, crashes follow some checkpoint. Build
    messages are assigned to partitions with the same function the engine
    uses for scan rows, so matching keys always co-locate."""
    key_pool = ["a", "b", "c", None, "d"]

    def partition_of(key):
        nkey = NULL if key is None else key
        return hash_fn(nkey, 0) % num_partitions

    keys_by_partition = {p: [k for k in key_pool if partition_of(k) == p]
                         for p in range(num_partitions)}
    streams = {}          # (partition, epoch) -> [BuildData]
    for p in range(num_partitions):
        pool = keys_by_partition[p]
        for epoch in (1, 2):
            n = rng.randint(0, 3) if pool else 0
            msgs = [BuildData(p, epoch, seq, rng.choice(pool), f"b{p}e{epoch}s{seq}")
                    for seq in range(1, n + 1)]
            streams[(p, epoch)] = msgs

    events = []
    unlocked = {(p, 1) for p in range(num_partitions)}
    pending = []
    for (p, epoch), msgs in streams.items():
        deliveries = list(msgs) + [BuildBarrier(p, epoch, len(msgs))]
        deliveries += rng.sample(msgs, k=min(len(msgs), 1))  # redelivery
        rng.shuffle(deliveries)
        pending.append(((p, epoch), [Deliver(m) for m in deliveries]))

    batches = [(f"s{i}", [(rng.choice(key_pool), f"v{i}r{j}")
                          for j in range(rng.randint(0, 4))])
               for i in range(num_batches)]
    batch_iter = iter(batches)
    submitted = []

    def random_control():
        roll = rng.random()
        if roll < 0.08:
            return Checkpoint()
        if roll < 0.11:
            return Crash()
        if roll < 0.16:
            return AckAll()
        if roll < 0.20:
            return Poll()
        return None

    guard = 0
    while (pending or batch_iter is not None) and guard < 10_000:
        guard += 1
        ctrl = random_control()
        if isinstance(ctrl, Crash) and not any(isinstance(e, Checkpoint) for e in events):
            events.append(Checkpoint())
        if ctrl is not None:
            events.append(ctrl)
            continue
        choice = rng.random()
        if pending and (choice < 0.6 or batch_iter is None):
            idx = rng.randrange(len(pending))
            (p, epoch), evs = pending[idx]
            if (p, epoch) not in unlocked:
                events.append(Rebuild(p, epoch))
                unlocked.add((p, epoch))
            events.append(evs.pop(0))
            if not evs:
                pending.pop(idx)
        else:
            try:
                batch = next(batch_iter)
            except StopIteration:
                batch_iter = None
                continue
            events.append(Submit(*batch))
            submitted.append(batch)
    events += [Poll(), AckAll()]
    return events, streams, submitted


def reference_for(submitted, streams, events, join_type, null_mode):
    """Bind each submitted batch to the epochs current at its submit point."""
    current = {}
    bound = {}
    for event in events:
        if isinstance(event, Rebuild):
            current[event.partition] = event.epoch
        elif isinstance(event, Submit):
            bound[event.batch_id] = dict(current)
    expected = Counter()
    for batch_id, rows in submitted:
        build_rows = []
        for (p, epoch), msgs in streams.items():
            if bound[batch_id].get(p, 1) != epoch:
                continue
            seen = set()
            for msg in msgs:
                if msg.seq not in seen:
                    seen.add(msg.seq)
                    build_rows.append((msg.key, msg.payload))
        expected.update(nested_loop_join(rows, build_rows, join_type, null_mode))
    return expected


def test_fuzz_against_nested_loop(tmp_path):
    rng = random.Random(20260923)
    for trial in range(40):
        num_partitions = rng.choice([1, 2, 3])
        strategy = rng.choice(list(Strategy))
        join_type = rng.choice([JoinType.INNER, JoinType.LEFT])
        null_mode = rng.choice([NullMode.REJECT, NullMode.SAFE])
        filter_kind = rng.choice(["exact", "bloom"])
        hash_fn = rng.choice([None, lambda k, s: 3])  # None -> default hash
        kwargs = {"num_partitions": num_partitions, "join_type": join_type,
                  "null_mode": null_mode, "strategy": strategy,
                  "filter_kind": filter_kind, "bloom_size": 64,
                  "bloom_hashes": 2}
        if hash_fn is not None:
            kwargs["hash_fn"] = hash_fn
        events, streams, submitted = generate_script(
            rng, num_partitions, num_batches=rng.randint(1, 4),
            hash_fn=hash_fn or default_hash)
        harness = Harness(kwargs, tmpdir=str(tmp_path / f"t{trial}"))
        harness.run(events)
        assert not harness.engine.batches, f"trial {trial}: unfinished batches"
        expected = reference_for(submitted, streams, events, join_type, null_mode)
        assert harness.consumer.multiset() == expected, f"trial {trial}"
        # old epochs released once nothing references them
        assert len(harness.engine.build.by_version) == num_partitions
        # temp files released once drained
        if harness.engine.spill is not None:
            assert harness.engine.spill.files() == []
