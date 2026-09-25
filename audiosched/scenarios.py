"""Replayable sample scenarios.

Each ``scenario_*`` function is deterministic: it builds an engine from the
local fixtures, applies a fixed script of operations, and returns a result
dict containing the emitted outputs, the audit trail, and a SHA-256 digest
of the concatenated output samples.  ``replay.py`` re-runs any scenario by
name; the unittest suite pins the digests.
"""

from __future__ import annotations

import hashlib
import struct

from .engine import Engine, MASTER
from .events import filter_event, gain_event, route_event
from .fixtures import (BLOCK_SIZE, DECLARED_LATENCY_SAMPLES,
                       NUMERIC_TOLERANCE, SAMPLE_RATE, constant,
                       impulse_train, sine)

TOTAL = BLOCK_SIZE * 8  # 512 samples of fixture material per track


def _tracks() -> dict[str, list[float]]:
    return {
        "drums": impulse_train(TOTAL, 16, 0.9),
        "bass": constant(TOTAL, 0.5),
        "lead": sine(TOTAL, 440.0, SAMPLE_RATE, 0.25),
    }


def _engine(routes=None, buses=()) -> Engine:
    return Engine(
        sample_rate=SAMPLE_RATE,
        block_size=BLOCK_SIZE,
        tracks=_tracks(),
        routes=routes if routes is not None
        else [(t, MASTER) for t in ("drums", "bass", "lead")],
        buses=tuple(buses),
        latency_samples=DECLARED_LATENCY_SAMPLES,
        tolerance=NUMERIC_TOLERANCE,
    )


def _digest(outputs) -> str:
    h = hashlib.sha256()
    for block in outputs:
        h.update(struct.pack("<qq", *block.output_id))
        h.update(struct.pack(f"<{len(block.samples)}d", *block.samples))
    return h.hexdigest()


def _result(engine: Engine, extra=None) -> dict:
    return {
        "outputs": engine.emitted,
        "audit": list(engine.audit),
        "digest": _digest(engine.emitted),
        "extra": extra or {},
    }


def scenario_interleave() -> dict:
    """Complex interleaving: mid-block gain/filter/route events, a
    same-position transaction, and an exact block-boundary event."""
    eng = _engine()
    eng.submit(gain_event("g1", 0, 30, 1, "bass", 0.2))          # mid-block
    eng.submit(filter_event("f1", 0, 30, 1, "lead", 2000.0))     # same pos
    eng.submit(gain_event("g2", 0, 64, 1, "drums", 0.5))         # boundary
    eng.submit(route_event("r1", 0, 100, 1, "disconnect",
                           "bass", MASTER))
    eng.submit(gain_event("g3", 0, 100, 1, "lead", 2.0))         # same pos
    eng.submit(filter_event("f2", 0, 150, 1, "drums", 800.0))
    eng.render_blocks(8)
    return _result(eng)


def scenario_transaction_cycle() -> dict:
    """Same-sample route swap must commit atomically (no transient cycle);
    a genuinely cyclic transaction must roll back entirely."""
    eng = _engine(routes=[("drums", "bass_bus"), ("bass_bus", MASTER),
                          ("bass", MASTER), ("lead", MASTER)],
                  buses=("bass_bus",))
    # At sample 96: swap drums->bass_bus into bass_bus->drums style loop that
    # is only legal because the old edge is removed in the SAME transaction.
    eng.submit(route_event("swap1", 0, 96, 1, "disconnect",
                           "drums", "bass_bus"))
    eng.submit(route_event("swap2", 0, 96, 1, "connect",
                           "bass_bus", "drums"))
    eng.submit(route_event("swap3", 0, 96, 1, "connect",
                           "drums", MASTER))
    # At sample 200: illegal transaction (would create drums<->bass_bus loop
    # because a parallel edge is re-added without removing the reverse one).
    eng.submit(route_event("bad1", 0, 200, 1, "connect",
                           "drums", "bass_bus"))
    eng.render_blocks(8)
    return _result(eng)


def scenario_epoch_boundary() -> dict:
    """Old-epoch events are quarantined after an epoch switch; a block
    boundary event fires exactly once."""
    eng = _engine()
    eng.submit(gain_event("old1", 0, 100, 1, "bass", 0.1))
    eng.render_blocks(1)                      # clock = 64
    eng.set_epoch(1)                          # old1 quarantined
    eng.submit(gain_event("new1", 1, 64, 1, "bass", 0.3))   # boundary
    eng.submit(gain_event("stale", 0, 70, 1, "lead", 9.0))  # wrong epoch
    eng.render_blocks(4)
    return _result(eng)


def scenario_late_correction() -> dict:
    """A higher-version correction rewrites unconfirmed output; a
    correction targeting confirmed output yields an audit conflict."""
    eng = _engine()
    eng.submit(gain_event("c1", 0, 100, 1, "bass", 0.2))
    eng.render_blocks(4)                      # blocks 0..3 emitted
    # v2 moves the gain change earlier; blocks 1..3 not yet confirmed.
    eng.submit(gain_event("c1", 0, 70, 2, "bass", 0.4))
    eng.render_blocks(3)                      # re-render blocks 1..3
    eng.confirm((0, 0))
    eng.confirm((0, 64))
    # Too late: block covering sample 70 is confirmed now.
    eng.submit(gain_event("c1", 0, 70, 3, "bass", 0.9))
    eng.render_blocks(2)
    return _result(eng)


def scenario_pause_resume() -> dict:
    """Pause lands after event application but before confirmation; resume
    de-duplicates outputs by id after a rollback re-render."""
    eng = _engine()
    eng.submit(gain_event("p1", 0, 70, 1, "lead", 3.0))
    eng.render_blocks(2)                      # blocks 0,1 emitted
    eng.pause()
    paused_render = eng.render_block()        # must be None
    eng.confirm((0, 0))                       # block 0 played
    eng.resume()
    # Late correction into unconfirmed block 1 forces a rollback that also
    # re-renders confirmed block 0 internally; emission must de-dup it.
    eng.submit(gain_event("p2", 0, 100, 1, "bass", 0.25))
    eng.render_blocks(3)
    return _result(eng, extra={"paused_render": paused_render})


SCENARIOS = {
    "interleave": scenario_interleave,
    "transaction_cycle": scenario_transaction_cycle,
    "epoch_boundary": scenario_epoch_boundary,
    "late_correction": scenario_late_correction,
    "pause_resume": scenario_pause_resume,
}
