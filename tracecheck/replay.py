"""Replay a visible word on a machine, exposing the subset trajectory.

This is what makes a counterexample replayable: given only the model and
the word, anyone can recompute the evidence.
"""
from __future__ import annotations

from .determinize import Determinizer


def replay(machine, word):
    """Return the subset after each prefix of ``word`` (including the empty prefix)."""
    det = Determinizer(machine)
    trajectory = [det.initial_subset()]
    current = trajectory[0]
    for action in word:
        current = det.step(current, action)
        trajectory.append(current)
    return trajectory


def accepts(machine, word) -> bool:
    return bool(replay(machine, word)[-1])


def check_witness(left, right, word, side) -> bool:
    """Verify that ``word`` is a trace of exactly the claimed side."""
    left_ok = accepts(left, word)
    right_ok = accepts(right, word)
    if side == "left_only":
        return left_ok and not right_ok
    if side == "right_only":
        return right_ok and not left_ok
    raise ValueError(f"unknown side {side!r}")
