"""Replay verification: re-execute a recorded run and compare outcomes."""
from __future__ import annotations

from .solver import segment_signature, solve


def replay_solve(facts, rules, query, result, max_atomic_segments=None, max_combinations=None) -> bool:
    """Re-solve from the same inputs and check that both the segments and
    every recorded journal step match the original evidence."""
    fresh = solve(
        facts,
        rules,
        query,
        max_atomic_segments=max_atomic_segments,
        max_combinations=max_combinations,
    )
    if segment_signature(fresh.segments) != segment_signature(result.segments):
        return False
    fresh_steps = [(s["step_id"], s["version"], s["kind"], s["payload"]) for s in fresh.journal.steps]
    given_steps = [(s["step_id"], s["version"], s["kind"], s["payload"]) for s in result.journal.steps]
    return fresh_steps == given_steps
