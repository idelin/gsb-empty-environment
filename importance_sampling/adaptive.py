"""Adaptive importance sampling with checkpointing.

Each iteration may use a different proposal.  The log-weight of every
sample is computed once, at draw time, against the proposal that actually
generated it, and is stored alongside the sample.  Checkpoints persist
those weights verbatim: restoring a checkpoint and continuing with a new
proposal never recomputes old weights against the new density.

Pooling rule: the pooled ordinary IS estimate averages w_i f(x_i) over
all iterations.  EXACT property: it stays unbiased as long as *every*
iteration's proposal covers the target, because each term satisfies
E_{q_t}[w f] = E_p[f] individually.  An iteration with zero coverage is
reported and makes the pooled estimates unavailable.
"""

from __future__ import annotations

import json
import math
from dataclasses import asdict, dataclass
from typing import Any, Callable, Hashable, List, Optional

from .estimators import (
    Diagnostic,
    ISResult,
    check_coverage,
    diagnose_log_weights,
    effective_sample_size,
    ordinary_is,
    self_normalized_is,
    _log_pdf_of,
)

CHECKPOINT_VERSION = 1


@dataclass
class WeightedSample:
    """A sample plus the log-weight frozen at its draw time."""

    x: Hashable
    log_weight: float
    proposal_id: str
    iteration: int


class AdaptiveIS:
    """Collects weighted samples across iterations of changing proposals."""

    def __init__(self, target: Any, *, seed: Optional[int] = None):
        self.target = target
        self.seed = seed
        self.samples: List[WeightedSample] = []
        self.diagnostics: List[Diagnostic] = []

    @property
    def n(self) -> int:
        return len(self.samples)

    def run_iteration(self, proposal: Any, n: int, rng) -> int:
        """Draw ``n`` samples from ``proposal``; returns the iteration index.

        Weights are computed against ``proposal`` right now and stored.
        """
        if n < 1:
            raise ValueError("n must be >= 1")
        iteration = max((s.iteration for s in self.samples), default=-1) + 1
        proposal_id = getattr(proposal, "name", None) or f"proposal-{iteration}"
        for d in check_coverage(self.target, proposal):
            self.diagnostics.append(
                Diagnostic(d.code, d.severity, f"[iteration {iteration}, {proposal_id}] {d.message}")
            )
        for _ in range(n):
            x = proposal.sample(rng)
            log_weight = _log_pdf_of(self.target, x) - _log_pdf_of(proposal, x)
            self.samples.append(WeightedSample(x, log_weight, proposal_id, iteration))
        return iteration

    def estimate(self, f: Callable[[Hashable], float], *, ess_warn_fraction: float = 0.1) -> ISResult:
        """Pooled ordinary / self-normalized estimates over all samples."""
        diagnostics = list(self.diagnostics)
        if not self.samples:
            diagnostics.append(Diagnostic("NO_SAMPLES", "error", "no samples have been collected"))
            return ISResult(None, None, float("nan"), 0, self.seed, diagnostics, available=False)
        log_weights = [s.log_weight for s in self.samples]
        diagnostics.extend(diagnose_log_weights(log_weights))
        if any(d.severity == "error" for d in diagnostics):
            return ISResult(None, None, float("nan"), self.n, self.seed, diagnostics, available=False)
        f_values = [float(f(s.x)) for s in self.samples]
        ess = effective_sample_size(log_weights)
        if ess < ess_warn_fraction * self.n:
            diagnostics.append(
                Diagnostic(
                    "LOW_ESS",
                    "warning",
                    f"effective sample size {ess:.1f} is below {ess_warn_fraction:.0%} "
                    f"of n={self.n}; estimates may be unreliable",
                )
            )
        return ISResult(
            ordinary=ordinary_is(log_weights, f_values),
            self_normalized=self_normalized_is(log_weights, f_values),
            ess=ess,
            n=self.n,
            seed=self.seed,
            diagnostics=diagnostics,
            available=True,
        )

    def save_checkpoint(self, path: str) -> None:
        """Persist samples (with frozen weights), diagnostics, seed and n."""
        payload = {
            "version": CHECKPOINT_VERSION,
            "seed": self.seed,
            "n": self.n,
            "samples": [asdict(s) for s in self.samples],
            "diagnostics": [asdict(d) for d in self.diagnostics],
        }
        with open(path, "w", encoding="utf-8") as fh:
            json.dump(payload, fh, indent=2)

    @classmethod
    def load_checkpoint(cls, path: str, target: Any) -> "AdaptiveIS":
        """Restore from a checkpoint.

        Stored log-weights are reused exactly as saved; they are NOT
        recomputed against any current or future proposal density.
        """
        with open(path, "r", encoding="utf-8") as fh:
            payload = json.load(fh)
        if payload.get("version") != CHECKPOINT_VERSION:
            raise ValueError(f"unsupported checkpoint version: {payload.get('version')!r}")
        restored = cls(target, seed=payload.get("seed"))
        restored.samples = [WeightedSample(**s) for s in payload["samples"]]
        restored.diagnostics = [Diagnostic(**d) for d in payload["diagnostics"]]
        return restored
