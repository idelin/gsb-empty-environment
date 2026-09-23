"""Exact vs Bloom filters, injectable colliding hash, false-positive budget."""
from collections import Counter

from dynjoin import (BloomFilter, BuildBarrier, BuildData, Deliver, ExactFilter,
                     Harness, JoinType, NullMode, Poll, Strategy, Submit)
from dynjoin.reference import nested_loop_join


def constant_hash(key, seed):
    return 7  # every key collides everywhere


def test_bloom_with_colliding_hash_admits_everything():
    bloom = BloomFilter(size=64, num_hashes=3, hash_fn=constant_hash)
    bloom.add("a")
    assert bloom.might_contain("a")
    assert bloom.might_contain("never-inserted")  # false positive by collision


def test_exact_filter_has_no_false_positives():
    filt = ExactFilter()
    filt.add("a")
    assert filt.might_contain("a")
    assert not filt.might_contain("b")


def run_join(tmp_path, hash_fn, filter_kind, subdir):
    events = [
        Deliver(BuildData(0, 1, 1, "a", "b1")),
        Deliver(BuildData(0, 1, 2, "b", "b2")),
        Deliver(BuildBarrier(0, 1, 2)),
        Submit("s0", [("a", "s1"), ("zzz", "s2"), ("b", "s3")]),
        Poll(),
    ]
    harness = Harness(
        {"num_partitions": 1, "join_type": JoinType.INNER,
         "null_mode": NullMode.REJECT, "strategy": Strategy.WAIT,
         "filter_kind": filter_kind, "hash_fn": hash_fn},
        tmpdir=str(tmp_path / subdir))
    harness.run(events)
    return harness


def test_false_positives_only_add_work(tmp_path):
    scan_rows = [("a", "s1"), ("zzz", "s2"), ("b", "s3")]
    build_rows = [("a", "b1"), ("b", "b2")]
    expected = Counter(nested_loop_join(scan_rows, build_rows,
                                        JoinType.INNER, NullMode.REJECT))

    exact = run_join(tmp_path, lambda k, s: hash(k), "exact", "exact")
    bloom = run_join(tmp_path, constant_hash, "bloom", "bloom")

    # identical results either way
    assert exact.consumer.multiset() == expected
    assert bloom.consumer.multiset() == expected
    # the colliding Bloom filtered nothing out: strictly more table probes
    assert bloom.engine.stats["filter_negatives"] == 0
    assert bloom.engine.stats["table_probes"] == 3
    assert exact.engine.stats["filter_negatives"] == 1  # "zzz" filtered
    assert exact.engine.stats["table_probes"] == 2
