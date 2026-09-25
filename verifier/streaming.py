"""Streaming execution, chunk/shift counterexample search.

Contract for an implementation under test (synchronous streaming):
    reset()                  -> None; back to a pristine state
    process(chunk) -> list   -> exactly len(chunk) output samples
    finalize()     -> list   -> any pending tail samples (may be empty)
    latency_samples          -> declared latency, in samples

The verifier only ever talks to a *factory* producing fresh instances,
so unknown stateful implementations can be probed repeatedly.
"""
from __future__ import annotations

import json
import random
from dataclasses import dataclass


def run_streaming(impl, x, boundaries=()):
    """Run impl over x, split at the given boundary positions."""
    impl.reset()
    n = len(x)
    pts = sorted({min(max(int(b), 0), n) for b in boundaries})
    out = []
    prev = 0
    for b in pts + [n]:
        out.extend(impl.process(x[prev:b]))
        prev = b
    out.extend(impl.finalize())
    return out


def compare_outputs(ref, out, tol: float):
    """Return (max_abs_diff, first_index_above_tol_or_None)."""
    n = min(len(ref), len(out))
    max_diff = 0.0
    first_idx = None
    for i in range(n):
        d = abs(ref[i] - out[i])
        if d > max_diff:
            max_diff = d
        if d > tol and first_idx is None:
            first_idx = i
    if len(ref) != len(out):
        extra = ref[n:] if len(ref) > n else out[n:]
        m = max((abs(v) for v in extra), default=0.0)
        if m > max_diff:
            max_diff = m
        if m > tol and first_idx is None:
            first_idx = n
    return max_diff, first_idx


def random_boundaries(n: int, n_chunks: int, rng: random.Random):
    if n_chunks <= 1 or n <= 1:
        return []
    k = min(n_chunks - 1, n - 1)
    return sorted(rng.sample(range(1, n), k))


@dataclass
class ChunkCounterexample:
    """Replayable evidence: one input, one chunk partition, one mismatch."""
    input: list
    boundaries: list
    max_abs_diff: float
    first_diff_index: int | None
    tolerance: float
    kind: str = "chunk_inconsistency"

    def still_fails(self, impl_factory):
        ref = run_streaming(impl_factory(), self.input, [])
        out = run_streaming(impl_factory(), self.input, self.boundaries)
        diff, _ = compare_outputs(ref, out, self.tolerance)
        return diff > self.tolerance, diff

    def to_dict(self):
        return {
            "kind": self.kind,
            "input": self.input,
            "boundaries": self.boundaries,
            "max_abs_diff": self.max_abs_diff,
            "first_diff_index": self.first_diff_index,
            "tolerance": self.tolerance,
        }

    def save(self, path):
        with open(path, "w") as fh:
            json.dump(self.to_dict(), fh)

    @classmethod
    def load(cls, path):
        with open(path) as fh:
            doc = json.load(fh)
        return cls(doc["input"], doc["boundaries"], doc["max_abs_diff"],
                   doc["first_diff_index"], doc["tolerance"])


@dataclass
class ShiftCounterexample:
    """Replayable evidence: output is not the time-shift of the reference."""
    input: list
    shift: int
    max_abs_diff: float
    first_diff_index: int | None
    tolerance: float
    kind: str = "phase_misalignment"

    def still_fails(self, impl_factory):
        base = run_streaming(impl_factory(), self.input, [])
        out = run_streaming(impl_factory(), [0.0] * self.shift + list(self.input), [])
        exp = [0.0] * self.shift + base
        diff, _ = compare_outputs(exp, out, self.tolerance)
        return diff > self.tolerance, diff

    def to_dict(self):
        return {
            "kind": self.kind,
            "input": self.input,
            "shift": self.shift,
            "max_abs_diff": self.max_abs_diff,
            "first_diff_index": self.first_diff_index,
            "tolerance": self.tolerance,
        }

    def save(self, path):
        with open(path, "w") as fh:
            json.dump(self.to_dict(), fh)

    @classmethod
    def load(cls, path):
        with open(path) as fh:
            doc = json.load(fh)
        return cls(doc["input"], doc["shift"], doc["max_abs_diff"],
                   doc["first_diff_index"], doc["tolerance"])


def find_chunk_counterexample(impl_factory, x, trials: int, seed: int, tol: float):
    """Random partition search. Returns (counterexample_or_None, worst_diff)."""
    x = list(x)
    ref = run_streaming(impl_factory(), x, [])
    rng = random.Random(seed)
    worst = 0.0
    for _ in range(trials):
        b = random_boundaries(len(x), rng.randint(2, 8), rng)
        out = run_streaming(impl_factory(), x, b)
        diff, idx = compare_outputs(ref, out, tol)
        worst = max(worst, diff)
        if diff > tol:
            return ChunkCounterexample(list(x), b, diff, idx, tol), worst
    return None, worst


def find_shift_counterexample(impl_factory, x, max_shift: int, tol: float):
    """Ascending shift search; the first failure is the minimal shift.
    Returns (counterexample_or_None, worst_diff)."""
    x = list(x)
    base = run_streaming(impl_factory(), x, [])
    worst = 0.0
    for k in range(1, max_shift + 1):
        out = run_streaming(impl_factory(), [0.0] * k + x, [])
        exp = [0.0] * k + base
        diff, idx = compare_outputs(exp, out, tol)
        worst = max(worst, diff)
        if diff > tol:
            return ShiftCounterexample(list(x), k, diff, idx, tol), worst
    return None, worst
