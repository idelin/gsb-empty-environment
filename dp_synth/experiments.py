"""Statistical diagnostics for the Laplace mechanism on synthetic data.

These checks are *empirical sanity checks*, not a formal privacy proof.
The guarantee comes from the calibrated Laplace mechanism
(Pr[M(d) in S] / Pr[M(d') in S] <= exp(epsilon) for neighbouring d, d');
the experiments below only demonstrate bias, scale and coverage.
"""

from __future__ import annotations

import math
import statistics
from dataclasses import dataclass
from typing import List

from .mechanism import Rng, laplace_mechanism
from .queries import CountQuery
from .synthetic_data import SyntheticData, make_synthetic_employee_data
from .engine import DPQueryEngine


@dataclass(frozen=True)
class MechanismDiagnostics:
    epsilon: float
    sensitivity: float
    expected_scale: float
    observed_bias: float
    observed_stddev: float
    empirical_coverage_epsilon: float
    within_epsilon_budget_fraction: float
    trials: int

    def as_text(self) -> str:
        return (
            f"trials={self.trials} epsilon={self.epsilon:g} scale={self.expected_scale:.4f} "
            f"bias={self.observed_bias:+.4f} stddev={self.observed_stddev:.4f}\n"
            f"empirical coverage of |noise|<=scale = {self.empirical_coverage_epsilon:.3f} "
            f"(theory 1-exp(-1)={1 - math.exp(-1):.3f}); "
            f"fraction of answers within +/-1 scale of truth = {self.within_epsilon_budget_fraction:.3f}"
        )


def diagnose_laplace(
    true_value: float = 1000.0,
    epsilon: float = 1.0,
    sensitivity: float = 1.0,
    trials: int = 20000,
    seed: int = 4242,
) -> MechanismDiagnostics:
    rng = Rng(seed)
    scale = sensitivity / epsilon
    query = CountQuery(epsilon=epsilon)
    noises: List[float] = []
    close = 0
    for _ in range(trials):
        answer = laplace_mechanism(true_value, epsilon, sensitivity, rng)
        noise = answer - true_value
        noises.append(noise)
        if abs(noise) <= scale:
            close += 1
    observed_bias = statistics.fmean(noises)
    observed_stddev = statistics.pstdev(noises)
    coverage = close / trials
    diagnostics = MechanismDiagnostics(
        epsilon=epsilon,
        sensitivity=sensitivity,
        expected_scale=scale,
        observed_bias=observed_bias,
        observed_stddev=observed_stddev,
        empirical_coverage_epsilon=coverage,
        within_epsilon_budget_fraction=coverage,
        trials=trials,
    )
    return diagnostics


def demonstrate_budget_semantics(data: SyntheticData | None = None) -> str:
    """Run a fixed scenario showing cache reuse vs. fresh charging."""
    data = data or make_synthetic_employee_data()
    engine = DPQueryEngine(total_epsilon_per_version=4.0, rng_seed=7)
    lines: List[str] = []

    query_a = CountQuery(epsilon=1.0)
    first = engine.run(query_a, data)
    second = engine.run(query_a, data)
    lines.append(
        f"count all:        new={first.value:9.3f} reuse={second.value:9.3f} "
        f"identical={first.value == second.value} remaining={engine.remaining_budget(data.version):g}"
    )
    return "\n".join(lines)
