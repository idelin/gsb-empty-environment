"""Counterexample search and shrinking for streaming/phase inconsistencies.

Two searches are provided:

- block inconsistency: the same probe signal is streamed in random block
  partitions; output must match one-shot processing within tolerance.
  A failing partition is shrunk by greedily merging blocks (ddmin-style
  on chunk boundaries) until it is 1-minimal.
- phase misalignment: the probe is shifted by k samples; output must be
  the k-shifted reference.  A failing shift is shrunk to the smallest
  failing k.

Every counterexample stores (seed, signal length, partition/shift,
tolerance, worst error, error index, evidence excerpt) so it can be
serialized to JSON and replayed exactly against any implementation.
"""
from __future__ import annotations

import json
import random
from dataclasses import asdict, dataclass, field
from typing import Callable

from .metrics import max_abs_diff
from .signals import probe_signal
from .streaming import run_blocks

ImplFactory = Callable[[], object]


@dataclass
class CounterExample:
    kind: str  # "block_inconsistency" | "phase_misalignment"
    seed: int
    sample_rate: float
    signal_length: int
    tolerance: float
    max_error: float
    error_index: int
    partition: list[int] | None = None
    shift: int | None = None
    evidence: dict = field(default_factory=dict)

    def replay(self, impl_factory: ImplFactory) -> tuple[bool, float]:
        """Re-run the stored failing case; returns (still_failing, max_error)."""
        signal = probe_signal(self.signal_length, self.sample_rate, self.seed)
        if self.kind == "block_inconsistency":
            ref = run_blocks(impl_factory(), signal, [self.signal_length])
            out = run_blocks(impl_factory(), signal, self.partition or [self.signal_length])
        elif self.kind == "phase_misalignment":
            k = self.shift or 0
            ref = run_blocks(impl_factory(), signal, [self.signal_length])
            shifted = [0.0] * k + signal
            out = run_blocks(impl_factory(), shifted, [len(shifted)])[k:]
        else:
            raise ValueError(f"unknown counterexample kind {self.kind!r}")
        err, _ = max_abs_diff(ref, out)
        return err > self.tolerance, err

    def to_json(self) -> str:
        return json.dumps(asdict(self), indent=2, sort_keys=True)

    @staticmethod
    def from_json(text: str) -> "CounterExample":
        return CounterExample(**json.loads(text))


def _evidence(ref: list[float], out: list[float], index: int, width: int = 4) -> dict:
    lo = max(0, index - width)
    hi = min(len(ref), index + width + 1)
    return {
        "around_error": [
            {"index": i, "reference": ref[i], "observed": out[i]} for i in range(lo, hi)
        ]
    }


def _shrink_partition(
    impl_factory: ImplFactory,
    signal: list[float],
    ref: list[float],
    partition: list[int],
    tol: float,
) -> tuple[list[int], float, int]:
    def fails(part: list[int]) -> tuple[bool, float, int]:
        out = run_blocks(impl_factory(), signal, part)
        err, idx = max_abs_diff(ref, out)
        return err > tol, err, idx

    current = list(partition)
    changed = True
    while changed and len(current) > 2:
        changed = False
        for i in range(len(current) - 1):
            candidate = current[:i] + [current[i] + current[i + 1]] + current[i + 2 :]
            ok, _, _ = fails(candidate)
            if ok:
                current = candidate
                changed = True
                break
    _, err, idx = fails(current)
    return current, err, idx


def search_block_inconsistency(
    impl_factory: ImplFactory,
    n: int,
    fs: float,
    tol: float,
    trials: int = 16,
    seed: int = 20240901,
) -> CounterExample | None:
    """Random-partition search; returns a shrunk counterexample or None."""
    signal = probe_signal(n, fs, seed)
    ref = run_blocks(impl_factory(), signal, [n])
    rng = random.Random(seed ^ 0x5EED)
    from .streaming import random_partition

    for _ in range(trials):
        part = random_partition(n, rng)
        out = run_blocks(impl_factory(), signal, part)
        err, idx = max_abs_diff(ref, out)
        if err > tol:
            shrunk, err, idx = _shrink_partition(impl_factory, signal, ref, part, tol)
            out = run_blocks(impl_factory(), signal, shrunk)
            return CounterExample(
                kind="block_inconsistency",
                seed=seed,
                sample_rate=fs,
                signal_length=n,
                tolerance=tol,
                max_error=err,
                error_index=idx,
                partition=shrunk,
                evidence=_evidence(ref, out, idx),
            )
    return None


def search_phase_misalignment(
    impl_factory: ImplFactory,
    n: int,
    fs: float,
    tol: float,
    seed: int = 20240902,
    max_shift: int = 32,
) -> CounterExample | None:
    """Shift-invariance search; returns a minimal-shift counterexample or None."""
    signal = probe_signal(n, fs, seed)
    ref = run_blocks(impl_factory(), signal, [n])
    candidates = [1, 2, 3, 4, 5, 6, 7, 8, 12, 16, 24, 32]
    candidates = [k for k in candidates if k <= max_shift]
    for k in candidates:
        shifted = [0.0] * k + signal
        out = run_blocks(impl_factory(), shifted, [len(shifted)])[k:]
        err, idx = max_abs_diff(ref, out)
        if err > tol:
            # shrink: find the smallest failing shift
            minimal = k
            for smaller in range(1, k):
                s2 = [0.0] * smaller + signal
                o2 = run_blocks(impl_factory(), s2, [len(s2)])[smaller:]
                e2, _ = max_abs_diff(ref, o2)
                if e2 > tol:
                    minimal = smaller
                    break
            shifted = [0.0] * minimal + signal
            out = run_blocks(impl_factory(), shifted, [len(shifted)])[minimal:]
            err, idx = max_abs_diff(ref, out)
            return CounterExample(
                kind="phase_misalignment",
                seed=seed,
                sample_rate=fs,
                signal_length=n,
                tolerance=tol,
                max_error=err,
                error_index=idx,
                shift=minimal,
                evidence=_evidence(ref, out, idx),
            )
    return None
