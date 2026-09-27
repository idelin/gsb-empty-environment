"""Bounded visible-trace comparison of two state machine versions.

The check runs an on-the-fly subset construction on both machines and a
breadth-first search over the product of subset spaces.  A word is
distinguishing as soon as one side's reachable subset is empty while the
other's is not; BFS order guarantees the shortest such word.  Nothing is
concluded about words longer than ``bound``: the verdict is only
"equivalent up to bound".  If the step budget runs out first, the verdict
stays UNKNOWN (pending).
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from enum import Enum
from typing import Optional, Tuple


class Verdict(Enum):
    EQUIVALENT_UP_TO_BOUND = "equivalent_up_to_bound"
    DIFFERENT = "different"
    UNKNOWN = "unknown"


@dataclass(frozen=True)
class Counterexample:
    """A shortest distinguishing visible word plus evidence: the reachable
    state subsets of both machines after reading the word."""

    word: Tuple[str, ...]
    trace_of_first: bool
    first_subset: frozenset
    second_subset: frozenset

    def replay(self, first, second) -> bool:
        """Re-run the word on both machines and confirm the evidence."""
        first_subset = first.replay(self.word)
        second_subset = second.replay(self.word)
        return (
            first_subset == self.first_subset
            and second_subset == self.second_subset
            and bool(first_subset) != bool(second_subset)
            and bool(first_subset) == self.trace_of_first
        )

    def explain(self) -> str:
        word = " ".join(self.word) if self.word else "<empty>"
        present = "first" if self.trace_of_first else "second"
        absent = "second" if self.trace_of_first else "first"
        return (
            f"word [{word}] is a visible trace of the {present} machine only\n"
            f"  first  reachable subset: {sorted(self.first_subset)}\n"
            f"  second reachable subset: {sorted(self.second_subset)}\n"
            f"  -> the {absent} machine cannot produce this word"
        )


@dataclass(frozen=True)
class ComparisonResult:
    verdict: Verdict
    bound: int
    counterexample: Optional[Counterexample]
    explored: int

    @property
    def distinguishing_word(self) -> Optional[Tuple[str, ...]]:
        return self.counterexample.word if self.counterexample else None


def compare(first, second, bound, max_steps=1_000_000) -> ComparisonResult:
    """Compare visible trace sets up to words of length ``bound``."""
    if bound < 0:
        raise ValueError("bound must be non-negative")
    alphabet = sorted(first.visible_alphabet | second.visible_alphabet)
    start = (first.start_subset(), second.start_subset())
    visited = {start}
    queue = deque([(start[0], start[1], ())])
    explored = 0
    while queue:
        if explored >= max_steps:
            return ComparisonResult(Verdict.UNKNOWN, bound, None, explored)
        subset_first, subset_second, word = queue.popleft()
        explored += 1
        if bool(subset_first) != bool(subset_second):
            cex = Counterexample(word, bool(subset_first), subset_first, subset_second)
            return ComparisonResult(Verdict.DIFFERENT, bound, cex, explored)
        if len(word) == bound or not subset_first:
            continue
        for label in alphabet:
            nxt = (
                first.subset_step(subset_first, label),
                second.subset_step(subset_second, label),
            )
            if nxt not in visited:
                visited.add(nxt)
                queue.append((nxt[0], nxt[1], word + (label,)))
    return ComparisonResult(Verdict.EQUIVALENT_UP_TO_BOUND, bound, None, explored)


class BehaviorComparator:
    """Caches comparison results keyed by both machines' version tokens and
    content fingerprints.  Updating either machine (new version or new
    content) misses the cache; UNKNOWN (pending) results are never cached."""

    def __init__(self):
        self._cache = {}
        self.hits = 0
        self.misses = 0

    @staticmethod
    def _key(first, second, bound):
        return (
            first.version,
            first.fingerprint(),
            second.version,
            second.fingerprint(),
            bound,
        )

    def compare(self, first, second, bound, max_steps=1_000_000) -> ComparisonResult:
        key = self._key(first, second, bound)
        cached = self._cache.get(key)
        if cached is not None:
            self.hits += 1
            return cached
        self.misses += 1
        result = compare(first, second, bound, max_steps=max_steps)
        if result.verdict is not Verdict.UNKNOWN:
            self._cache[key] = result
        return result

    def invalidate(self):
        self._cache.clear()
