"""Barrier, last-message, redelivery and reorder handling on the build side."""
from dynjoin import BuildBarrier, BuildData, BuildSide


def make_side(num_partitions=1):
    return BuildSide(num_partitions, filter_kind="exact")


def test_barrier_first_then_data_completes_in_order():
    side = make_side()
    bp = side.current[0]
    side.route_and_raise = None
    bp.set_barrier(2)  # barrier arrives before any data
    assert not bp.complete
    bp.apply(BuildData(0, 1, 2, "b", "p2"))  # out of order: seq 2 first
    assert not bp.complete  # seq 1 still missing
    bp.apply(BuildData(0, 1, 1, "a", "p1"))
    assert bp.complete


def test_duplicate_redelivery_is_idempotent():
    side = make_side()
    bp = side.current[0]
    assert bp.apply(BuildData(0, 1, 1, "a", "p1"))
    assert not bp.apply(BuildData(0, 1, 1, "a", "p1"))  # redelivery
    assert not bp.apply(BuildData(0, 1, 1, "a", "pX"))  # conflicting redelivery
    bp.set_barrier(1)
    assert bp.complete
    assert bp.probe("a") == [("a", "p1", 1)]  # applied exactly once


def test_out_of_order_application_preserves_apply_order():
    side = make_side()
    bp = side.current[0]
    bp.apply(BuildData(0, 1, 3, "c", "p3"))
    bp.apply(BuildData(0, 1, 1, "a", "p1"))
    bp.apply(BuildData(0, 1, 2, "b", "p2"))
    bp.set_barrier(3)
    assert bp.complete
    assert [k for k, _, _ in bp.probe("a")] == ["a"]


def test_unknown_epoch_messages_are_routed_to_none():
    side = make_side()
    assert side.route(BuildData(0, 99, 1, "a", "p")) is None
    assert side.route(BuildBarrier(0, 99, 1)) is None


def test_rebuild_isolates_epochs_and_releases_old_version():
    side = make_side()
    old = side.current[0]
    old.apply(BuildData(0, 1, 1, "a", "p1"))
    old.refs = 1  # a running scan batch still uses the old version
    new = side.new_partition(0, 2)
    side.current[0] = new
    side.by_version[(0, 2)] = new
    side.maybe_release(old)
    assert not old.released  # still referenced
    # late messages for the old epoch still land on the old partition
    assert side.route(BuildData(0, 1, 2, "b", "p2")) is old
    old.refs = 0
    side.maybe_release(old)
    assert old.released
    assert side.route(BuildData(0, 1, 3, "c", "p3")) is None  # epoch 1 gone
    assert side.route(BuildData(0, 2, 1, "x", "q1")) is new
