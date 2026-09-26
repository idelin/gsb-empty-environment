"""Bounded product search over the on-demand determinisations of two machines.

The search compares *visible trace sets*, not state correspondences: a pair
of reachable subsets diverges exactly when one side can perform a visible
action the other cannot.  BFS by word length guarantees the first
divergence found is a shortest distinguishing visible sequence.
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from enum import Enum

from .determinize import Determinizer
from .replay import replay


class Verdict(Enum):
    EQUIVALENT_WITHIN_BOUND = "equivalent_within_bound"
    DIFFERENT = "different"
    UNKNOWN = "unknown"  # search budget exhausted before the space was


@dataclass(frozen=True)
class Evidence:
    """Replayable proof of a divergence: subset trajectory on both sides."""

    word: tuple
    side: str  # "left_only" | "right_only"
    left_trajectory: tuple  # subset after each prefix, incl. the empty prefix
    right_trajectory: tuple


@dataclass(frozen=True)
class ComparisonResult:
    verdict: Verdict
    bound: int
    witness: tuple | None
    evidence: Evidence | None
    explored_pairs: int
    truncated: bool


def compare_bounded(left, right, bound, cache=None, max_pairs=100_000) -> ComparisonResult:
    """Compare visible traces of length <= ``bound`` of two machines.

    Returns DIFFERENT with a shortest distinguishing word as soon as one is
    found; EQUIVALENT_WITHIN_BOUND only when every subset pair reachable
    within the bound was checked (no claim is made beyond ``bound``);
    UNKNOWN when ``max_pairs`` is exhausted first.
    """
    det_left = Determinizer(left, cache)
    det_right = Determinizer(right, cache)
    alphabet = sorted(left.alphabet | right.alphabet)
    start = (det_left.initial_subset(), det_right.initial_subset())
    visited = {start}
    queue = deque([(start[0], start[1], ())])
    explored = 0
    truncated = False
    result = None
    while queue:
        sub_left, sub_right, word = queue.popleft()
        explored += 1
        if explored > max_pairs:
            truncated = True
            break
        if len(word) >= bound:
            continue
        for action in alphabet:
            next_left = det_left.step(sub_left, action)
            next_right = det_right.step(sub_right, action)
            if (not next_left) != (not next_right):
                witness = word + (action,)
                side = "left_only" if next_left else "right_only"
                evidence = Evidence(
                    word=witness,
                    side=side,
                    left_trajectory=tuple(replay(left, witness)),
                    right_trajectory=tuple(replay(right, witness)),
                )
                result = ComparisonResult(
                    Verdict.DIFFERENT, bound, witness, evidence, explored, False
                )
                break
            if next_left and next_right:
                pair = (next_left, next_right)
                if pair not in visited:
                    visited.add(pair)
                    queue.append((next_left, next_right, word + (action,)))
        if result is not None:
            break
    if result is None:
        verdict = Verdict.UNKNOWN if truncated else Verdict.EQUIVALENT_WITHIN_BOUND
        result = ComparisonResult(verdict, bound, None, None, explored, truncated)
    det_left.flush()
    det_right.flush()
    return result
