"""Independent invariant checks: walk live views and re-audit the ledger."""


def reachable_buffers(roots):
    """Traverse buffers leased by live views under roots -> {buffer_id: size}."""
    found = {}
    for batch in roots:
        if not batch.is_open:
            continue
        for col in batch.columns():
            if not col.is_open:
                continue
            for buf in col.buffers():
                found[buf.id] = buf.size
    return found


def assert_consistent(ledger, roots, exact=True):
    """Re-derive the ledger state from live views and verify invariants.

    Checks: live bytes equal the sum of unique live buffer sizes; peak never
    drops below live; no buffer leased by a live view has been reclaimed.
    With exact=True (no drained-but-borrowed buffers pending) the ledger's
    live set must equal the reachable set.
    """
    snap = ledger.snapshot()
    assert snap.live_bytes == sum(snap.live.values()), "byte accounting mismatch"
    assert snap.peak_bytes >= snap.live_bytes, "peak below live"
    reachable = reachable_buffers(roots)
    for buffer_id, size in reachable.items():
        assert buffer_id in snap.live, (
            f"buffer {buffer_id} reclaimed while leased by a live view"
        )
        assert snap.live[buffer_id] == size, f"buffer {buffer_id} size mismatch"
    if exact:
        assert set(snap.live) == set(reachable), (
            f"ledger/views mismatch: ledger={sorted(snap.live)} "
            f"views={sorted(reachable)}"
        )
    return snap
