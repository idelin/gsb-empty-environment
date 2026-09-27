"""binpower: exact binomial power references + labeled Monte Carlo estimates.

Conventions
-----------
- ``kind="exact"``     : computed by binomial tail-probability recursion.
- ``kind="empirical"`` : Monte Carlo estimate, ALWAYS paired with a
                         Clopper-Pearson interval, seed and trial count.
- The test rule (critical values / spending boundaries) is fixed before any
  simulation; only budget allocation may adapt.
- Zero-trial estimates are refused (``ValueError``).
- Randomness is injected by the caller via seeded streams; every scenario
  gets an independent, reproducible stream derived from the master seed.
"""

from .adaptive import AdaptiveAllocation, adaptive_allocate
from .design import MinSampleSize, min_sample_size
from .exact import (
    ExactPower,
    binomial_cdf_table,
    binomial_pmf_table,
    binomial_tail,
    critical_value,
    exact_power,
)
from .interval import clopper_pearson, regularized_beta
from .simulate import (
    RejectionRateEstimate,
    Scenario,
    StreamHandle,
    child_seed,
    run_scenarios,
    simulate_rejection_rate,
    spawn_streams,
)
from .spending import (
    GroupSequentialDesign,
    GroupSequentialEstimate,
    LookBoundary,
    group_sequential_design,
    group_sequential_power,
    obrien_fleming_spending,
    power_spending,
    simulate_group_sequential,
)

__all__ = [
    "AdaptiveAllocation",
    "ExactPower",
    "GroupSequentialDesign",
    "GroupSequentialEstimate",
    "LookBoundary",
    "MinSampleSize",
    "RejectionRateEstimate",
    "Scenario",
    "StreamHandle",
    "adaptive_allocate",
    "binomial_cdf_table",
    "binomial_pmf_table",
    "binomial_tail",
    "child_seed",
    "clopper_pearson",
    "critical_value",
    "exact_power",
    "group_sequential_design",
    "group_sequential_power",
    "min_sample_size",
    "obrien_fleming_spending",
    "power_spending",
    "regularized_beta",
    "run_scenarios",
    "simulate_group_sequential",
    "simulate_rejection_rate",
    "spawn_streams",
]

__version__ = "0.1.0"
