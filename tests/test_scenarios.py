"""Scenario tests: the four required explanations of a 250 -> 3 reading."""

import pytest

from counter_analysis import (
    CounterStore,
    DiffStatus,
    Sample,
    SourceConfig,
)

CFG = SourceConfig(bit_width=8, max_interval=60.0, config_version="v1")


def make_store() -> CounterStore:
    store = CounterStore()
    store.configure("dev", CFG)
    return store


def test_wrap_250_to_3_is_determined_increment_9():
    store = make_store()
    store.insert(Sample("a", "dev", event_time=0.0, seq=1, count=250))
    rev = store.insert(Sample("b", "dev", event_time=10.0, seq=2, count=3))

    (diff,) = rev.emitted  # the a -> b interval
    assert diff.status is DiffStatus.DETERMINED
    assert diff.delta == 9  # one wrap: 250 -> 255 -> 0 -> 3
    assert diff.left_id == "a" and diff.right_id == "b"
    assert diff.config_version == "v1"

    totals = store.totals("dev")
    assert totals.complete
    assert totals.determined_total == 9


def test_same_numbers_with_reset_flag_mean_increment_3():
    store = make_store()
    store.insert(Sample("a", "dev", event_time=0.0, seq=1, count=250))
    store.insert(Sample("b", "dev", event_time=10.0, seq=2, count=3, reset=True))

    (diff,) = [d for d in store.diffs("dev") if d.right_id == "b"]
    assert diff.status is DiffStatus.RESET
    assert diff.delta == 3  # restart at 0, counted up to 3

    totals = store.totals("dev")
    assert totals.complete
    assert totals.determined_total == 3  # same readings, different story


def test_long_gap_is_uncertain_and_never_dropped():
    store = make_store()
    store.insert(Sample("a", "dev", event_time=0.0, seq=1, count=250))
    store.insert(Sample("b", "dev", event_time=1000.0, seq=2, count=3))

    (diff,) = [d for d in store.diffs("dev") if d.right_id == "b"]
    assert diff.status is DiffStatus.UNCERTAIN
    assert diff.delta is None

    totals = store.totals("dev")
    assert not totals.complete
    assert totals.determined_total == 0  # nothing knowable claimed
    assert totals.uncertain_intervals == (("a", "b"),)


def test_backfill_into_determined_segment_conserves_total():
    store = make_store()
    store.insert(Sample("a", "dev", event_time=0.0, seq=1, count=250))
    store.insert(Sample("c", "dev", event_time=40.0, seq=3, count=3))
    before = store.totals("dev").determined_total
    assert before == 9

    # Late sample lands inside the determined a -> c interval.
    rev = store.insert(Sample("b", "dev", event_time=20.0, seq=2, count=254))

    assert [d.key for d in rev.invalidated] == [("a", "c")]
    assert [d.key for d in rev.emitted] == [("a", "b"), ("b", "c")]
    deltas = {d.key: d.delta for d in rev.emitted}
    assert deltas[("a", "b")] == 4
    assert deltas[("b", "c")] == 5
    assert store.totals("dev").determined_total == before  # 4 + 5 == 9


def test_backfill_into_long_gap_resolves_only_what_becomes_decidable():
    store = make_store()
    store.insert(Sample("a", "dev", event_time=0.0, seq=1, count=250))
    store.insert(Sample("d", "dev", event_time=120.0, seq=4, count=3))
    assert store.totals("dev").uncertain_intervals == (("a", "d"),)

    # One middle sample: left half decidable, right half still too long.
    rev = store.insert(Sample("b", "dev", event_time=30.0, seq=2, count=10))
    assert [d.key for d in rev.invalidated] == [("a", "d")]
    by_key = {d.key: d for d in rev.emitted}
    assert by_key[("a", "b")].status is DiffStatus.DETERMINED
    assert by_key[("a", "b")].delta == 16
    assert by_key[("b", "d")].status is DiffStatus.UNCERTAIN

    totals = store.totals("dev")
    assert not totals.complete
    assert totals.determined_total == 16
    assert totals.uncertain_intervals == (("b", "d"),)

    # Second backfill resolves the rest; nothing uncertain remains.
    store.insert(Sample("c", "dev", event_time=80.0, seq=3, count=200))
    totals = store.totals("dev")
    assert totals.complete
    assert totals.determined_total == 16 + 190 + 59  # 250->10->200->3 mod 256


def test_retraction_restores_previous_interval():
    store = make_store()
    store.insert(Sample("a", "dev", event_time=0.0, seq=1, count=250))
    store.insert(Sample("c", "dev", event_time=40.0, seq=3, count=3))
    store.insert(Sample("b", "dev", event_time=20.0, seq=2, count=254))

    rev = store.retract("dev", "b")
    assert [d.key for d in rev.invalidated] == [("a", "b"), ("b", "c")]
    assert [d.key for d in rev.emitted] == [("a", "c")]
    assert rev.emitted[0].delta == 9
    assert store.totals("dev").determined_total == 9


def test_same_time_tie_broken_by_seq_then_sample_id():
    store = make_store()
    store.insert(Sample("s2", "dev", event_time=5.0, seq=2, count=7))
    store.insert(Sample("s1", "dev", event_time=5.0, seq=1, count=10))
    # Larger seq is the later observation at equal event_time.
    keys = [d.key for d in store.diffs("dev")]
    assert keys == [(None, "s1"), ("s1", "s2")]
    diff = store.diffs("dev")[1]
    assert diff.delta == (7 - 10) % 256

    # Equal time AND seq: sample_id is the deterministic fallback.
    store.insert(Sample("s0", "dev", event_time=5.0, seq=1, count=1))
    keys = [d.key for d in store.diffs("dev")]
    assert keys == [(None, "s0"), ("s0", "s1"), ("s1", "s2")]


def test_duplicate_insert_and_unknown_retract_rejected():
    store = make_store()
    store.insert(Sample("a", "dev", event_time=0.0, seq=1, count=1))
    with pytest.raises(ValueError):
        store.insert(Sample("a", "dev", event_time=1.0, seq=2, count=2))
    with pytest.raises(KeyError):
        store.retract("dev", "nope")
