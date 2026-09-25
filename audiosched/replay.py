"""Replayable sample scenarios for the unified scheduler.

Every scenario is a deterministic function of the local fixtures, so
``run_all()`` reproduces exactly the same outputs and audit trails on
every run. Scenarios are also exercised by the unittest suite.
"""
from __future__ import annotations

import random

from .events import FILTER, GAIN, ROUTE, ControlEvent
from .fixtures import (BLOCK_SIZE, MASTER, SAMPLE_RATE, TRACKS, TOLERANCE,
                       make_sources)
from .reference import reference_render
from .scheduler import Scheduler


def build_interleave_events():
    """A dense, deterministic tangle of gain/filter/route events spread
    over six blocks, including same-sample transactions."""
    rng = random.Random(7)
    events = []
    positions = sorted(rng.sample(range(0, BLOCK_SIZE * 6), 24))
    for k, p in enumerate(positions):
        if k % 3 == 0:
            events.append(ControlEvent(
                f"g{k}", 0, p, 1, GAIN,
                {"track": TRACKS[k % len(TRACKS)],
                 "gain": round(rng.uniform(0.0, 2.0), 6)}))
        elif k % 3 == 1:
            events.append(ControlEvent(
                f"f{k}", 0, p, 1, FILTER,
                {"track": TRACKS[k % len(TRACKS)],
                 "cutoff": round(rng.uniform(80.0, 9000.0), 3)}))
        else:
            events.append(ControlEvent(
                f"m{k}", 0, p, 1, GAIN,
                {"track": MASTER, "gain": round(rng.uniform(0.2, 1.0), 6)}))
    # Atomic routing re-wires (final graphs are always valid DAGs).
    events.append(ControlEvent("r1", 0, positions[3], 1, ROUTE,
                               {"op": "connect", "src": "b", "dst": "a"}))
    events.append(ControlEvent("r2", 0, positions[11], 1, ROUTE,
                               {"op": "disconnect", "src": "b", "dst": "a"}))
    events.append(ControlEvent("r3", 0, positions[11], 1, ROUTE,
                               {"op": "connect", "src": "a", "dst": "b"}))
    events.append(ControlEvent("r4", 0, positions[17], 1, ROUTE,
                               {"op": "disconnect", "src": "a", "dst": "b"}))
    return events


def _new_scheduler():
    return Scheduler(SAMPLE_RATE, BLOCK_SIZE, TRACKS, make_sources(), MASTER)


def _max_err(a, b):
    return max((abs(x - y) for x, y in zip(a, b)), default=0.0)


def scenario_complex_interleave():
    """Dense interleaving verified sample-by-sample against the reference."""
    sources = make_sources()
    sched = Scheduler(SAMPLE_RATE, BLOCK_SIZE, TRACKS, sources, MASTER)
    events = build_interleave_events()
    for e in events:
        sched.submit(e)
    rendered = []
    for _ in range(6):
        rendered.extend(sched.render_block().samples)
    expected = reference_render(SAMPLE_RATE, TRACKS, sources, MASTER,
                                events, BLOCK_SIZE * 6)
    err = _max_err(rendered, expected)
    return {
        "log": [f"submitted {len(events)} events over 6 blocks",
                f"rendered {len(rendered)} samples",
                f"max |sched - reference| = {err:.3e}"],
        "checks": {"matches_reference_within_tolerance": err <= TOLERANCE},
        "audit": [a.message for a in sched.audit_log],
    }


def scenario_atomic_route_swap():
    """Same-sample route pairs: a valid swap commits atomically; a pair
    whose final graph is a ring rolls back without ever being rendered."""
    sources = make_sources()
    sched = Scheduler(SAMPLE_RATE, BLOCK_SIZE, TRACKS, sources, MASTER)
    p_swap, p_ring = 40, 200
    events = [
        ControlEvent("pre", 0, 10, 1, ROUTE,
                     {"op": "connect", "src": "a", "dst": "b"}),
        # Direction swap a->b becomes b->a in one transaction. Applying
        # connect-first would transiently ring a<->b; the atomic commit
        # only ever exposes the final, legal DAG.
        ControlEvent("sw1", 0, p_swap, 1, ROUTE,
                     {"op": "connect", "src": "b", "dst": "a"}),
        ControlEvent("sw2", 0, p_swap, 1, ROUTE,
                     {"op": "disconnect", "src": "a", "dst": "b"}),
        # This pair's *final* graph is a ring: whole transaction rejected.
        ControlEvent("bad1", 0, p_ring, 1, ROUTE,
                     {"op": "connect", "src": "a", "dst": "b"}),
        ControlEvent("bad2", 0, p_ring, 1, ROUTE,
                     {"op": "disconnect", "src": "b", "dst": "master"}),
    ]
    for e in events:
        sched.submit(e)
    rendered = []
    for _ in range(3):
        rendered.extend(sched.render_block().samples)
    expected = reference_render(SAMPLE_RATE, TRACKS, sources, MASTER,
                                events, BLOCK_SIZE * 3)
    err = _max_err(rendered, expected)
    outcomes = {eid: oc for eid, _, _, oc in sched.applied_log}
    return {
        "log": [f"swap at {p_swap}: {outcomes.get('sw1')}/{outcomes.get('sw2')}",
                f"ring at {p_ring}: {outcomes.get('bad1')}/{outcomes.get('bad2')}",
                f"max |sched - reference| = {err:.3e}"],
        "checks": {
            "swap_committed": (outcomes.get("sw1") == "committed"
                               and outcomes.get("sw2") == "committed"),
            "ring_rolled_back": (outcomes.get("bad1") == "rolled_back"
                                 and outcomes.get("bad2") == "rolled_back"),
            "matches_reference_within_tolerance": err <= TOLERANCE,
        },
        "audit": [a.message for a in sched.audit_log],
    }


def scenario_transaction_rollback():
    """One illegal parameter aborts the whole same-sample transaction."""
    sched = _new_scheduler()
    pos = 50
    sched.submit(ControlEvent("ok-filter", 0, pos, 1, FILTER,
                              {"track": "a", "cutoff": 900.0}))
    sched.submit(ControlEvent("bad-gain", 0, pos, 1, GAIN,
                              {"track": "b", "gain": -0.5}))
    sched.render_block()
    outcomes = {eid: oc for eid, _, _, oc in sched.applied_log}
    rollbacks = [a for a in sched.audit_log if a.kind == "rollback"]
    return {
        "log": [f"transaction at {pos}: {outcomes}",
                f"audit: {[a.message for a in rollbacks]}"],
        "checks": {
            "both_rolled_back": (outcomes.get("ok-filter") == "rolled_back"
                                 and outcomes.get("bad-gain") == "rolled_back"),
            "rollback_audited": len(rollbacks) == 1,
        },
        "audit": [a.message for a in sched.audit_log],
    }


def scenario_epoch_change():
    """After an epoch switch, stale-epoch events never fire."""
    sched = _new_scheduler()
    sched.submit(ControlEvent("old-epoch-gain", 0, 200, 1, GAIN,
                              {"track": "a", "gain": 0.1}))
    sched.render_block()
    sched.set_epoch(1)
    sched.submit(ControlEvent("old-epoch-late", 0, 300, 1, GAIN,
                              {"track": "b", "gain": 0.1}))
    sched.submit(ControlEvent("new-epoch", 1, 300, 1, GAIN,
                              {"track": "b", "gain": 0.5}))
    sched.render_block()
    sched.render_block()
    fired = {eid for eid, _, _, oc in sched.applied_log if oc == "committed"}
    drops = [a for a in sched.audit_log if a.kind == "epoch_drop"]
    return {
        "log": [f"fired: {sorted(fired)}",
                f"epoch drops: {[a.message for a in drops]}"],
        "checks": {
            "old_epoch_never_fired": ("old-epoch-gain" not in fired
                                      and "old-epoch-late" not in fired),
            "new_epoch_fired": "new-epoch" in fired,
            "drops_audited": len(drops) == 2,
        },
        "audit": [a.message for a in sched.audit_log],
    }


def scenario_late_correction():
    """Confirmed output -> audit conflict; unconfirmed -> recompute."""
    sources = make_sources()
    sched = Scheduler(SAMPLE_RATE, BLOCK_SIZE, TRACKS, sources, MASTER)
    sched.submit(ControlEvent("g", 0, 10, 1, GAIN,
                              {"track": "a", "gain": 1.5}))
    blk0 = sched.render_block()
    blk1 = sched.render_block()
    sched.confirm(blk0.output_id)  # block 0 is now "played"
    conflict = sched.submit(
        ControlEvent("g", 0, 10, 2, GAIN, {"track": "a", "gain": 0.25}))
    conflicts = [a for a in sched.audit_log if a.kind == "conflict"]
    # A correction landing in the still-unconfirmed block 1 recomputes it.
    recomputed = sched.submit(
        ControlEvent("h", 0, 150, 1, GAIN, {"track": "b", "gain": 0.5}))
    events = [ControlEvent("g", 0, 10, 1, GAIN, {"track": "a", "gain": 1.5}),
              ControlEvent("h", 0, 150, 1, GAIN, {"track": "b", "gain": 0.5})]
    expected = reference_render(SAMPLE_RATE, TRACKS, sources, MASTER,
                                events, BLOCK_SIZE * 2)
    new_blk1 = {o.output_id: o
                for o in sched.pending_outputs()}[blk1.output_id]
    err = _max_err(new_blk1.samples, expected[BLOCK_SIZE:2 * BLOCK_SIZE])
    return {
        "log": ["correction into played block 0 -> conflict, untouched",
                "correction into pending block 1 -> recomputed",
                f"max |recomputed - reference| = {err:.3e}"],
        "checks": {
            "confirmed_correction_rejected": conflict is False,
            "conflict_audited": len(conflicts) == 1,
            "unconfirmed_recomputed": recomputed is True,
            "recompute_matches_reference": err <= TOLERANCE,
        },
        "audit": [a.message for a in sched.audit_log],
    }


def scenario_pause_resume():
    """Pause after events applied but before confirm; resume dedups by
    output id while a late correction recomputes the paused block."""
    sources = make_sources()
    sched = Scheduler(SAMPLE_RATE, BLOCK_SIZE, TRACKS, sources, MASTER)
    sched.submit(ControlEvent("g", 0, 10, 1, GAIN,
                              {"track": "a", "gain": 1.5}))
    blk0 = sched.render_block()      # events applied, output pending
    sched.pause()                    # paused before confirm
    blocked = sched.render_block()   # -> None while paused
    sched.submit(ControlEvent("g", 0, 10, 2, GAIN,
                              {"track": "a", "gain": 0.25}))  # recompute
    sched.resume()
    blk1 = sched.render_block()
    events = [ControlEvent("g", 0, 10, 2, GAIN,
                           {"track": "a", "gain": 0.25})]
    expected = reference_render(SAMPLE_RATE, TRACKS, sources, MASTER,
                                events, BLOCK_SIZE * 2)
    pending = {o.output_id: o for o in sched.pending_outputs()}
    err0 = _max_err(pending[blk0.output_id].samples, expected[:BLOCK_SIZE])
    err1 = _max_err(blk1.samples, expected[BLOCK_SIZE:])
    ids = [o.output_id for o in sched.pending_outputs()]
    return {
        "log": ["paused after apply/before confirm; render blocked",
                "correction recomputed pending block 0 in place",
                "resumed; block 1 rendered; output ids deduplicated"],
        "checks": {
            "render_blocked_while_paused": blocked is None,
            "block0_corrected_in_place": err0 <= TOLERANCE,
            "block1_matches_reference": err1 <= TOLERANCE,
            "output_ids_unique": len(ids) == len(set(ids)),
            "no_confirmed_reemit": all(
                sched.emit_log.count(o) == 1 for o in sched.emit_log
                if sched.is_confirmed(o)),
        },
        "audit": [a.message for a in sched.audit_log],
    }


def scenario_checkpoint_restore():
    """Named checkpoints make scheduling state re-derivable."""
    sched = _new_scheduler()
    sched.render_block()
    sched.checkpoint("cp")
    sched.submit(ControlEvent("g", 0, 200, 1, GAIN,
                              {"track": "a", "gain": 0.5}))
    first = sched.render_block().samples
    sched.restore("cp")
    second = sched.render_block().samples
    return {
        "log": ["rendered block 1, restored checkpoint, re-rendered",
                f"identical: {first == second}"],
        "checks": {"restore_reproduces_output": first == second},
        "audit": [a.message for a in sched.audit_log],
    }


def scenario_boundary_event():
    """An event exactly on a block boundary fires exactly once."""
    sources = make_sources()
    sched = Scheduler(SAMPLE_RATE, BLOCK_SIZE, TRACKS, sources, MASTER)
    events = [ControlEvent("edge", 0, BLOCK_SIZE, 1, GAIN,
                           {"track": "a", "gain": 0.5})]
    for e in events:
        sched.submit(e)
    rendered = []
    for _ in range(3):
        rendered.extend(sched.render_block().samples)
    expected = reference_render(SAMPLE_RATE, TRACKS, sources, MASTER,
                                events, BLOCK_SIZE * 3)
    err = _max_err(rendered, expected)
    fires = [eid for eid, _, _, oc in sched.applied_log if oc == "committed"]
    return {
        "log": [f"event at sample {BLOCK_SIZE} fired {fires.count('edge')}x",
                f"max |sched - reference| = {err:.3e}"],
        "checks": {
            "fired_exactly_once": fires.count("edge") == 1,
            "matches_reference_within_tolerance": err <= TOLERANCE,
        },
        "audit": [a.message for a in sched.audit_log],
    }


SCENARIOS = {
    "complex_interleave": scenario_complex_interleave,
    "atomic_route_swap": scenario_atomic_route_swap,
    "transaction_rollback": scenario_transaction_rollback,
    "epoch_change": scenario_epoch_change,
    "late_correction": scenario_late_correction,
    "pause_resume": scenario_pause_resume,
    "checkpoint_restore": scenario_checkpoint_restore,
    "boundary_event": scenario_boundary_event,
}


def run_all():
    results = {}
    for name, fn in SCENARIOS.items():
        try:
            results[name] = fn()
        except Exception as exc:  # keep replay going, surface the failure
            results[name] = {"log": [f"EXCEPTION: {exc!r}"],
                             "checks": {"no_exception": False},
                             "audit": []}
    return results
