"""Permutation tests over observation identities.

- Exact small-sample tests enumerate the full legal permutation group
  (independent two-sample, paired sign-flip, or stratified), defined
  over observation indices so tied values keep their multiplicity.
- Large-sample Monte Carlo mode uses a caller-injected random source,
  records the seed and draw count, and reports an explicit confidence
  interval instead of a pseudo-exact value.
"""
from .api import paired_test, stratified_test, two_sample_test
from .exact import (
    ExactAccumulator,
    ExactResult,
    accumulate_shard,
    exact_test,
    is_extreme,
    shard_ranges,
)
from .montecarlo import MonteCarloResult, monte_carlo_test, wilson_interval
from .spaces import (
    IndependentTwoSampleSpace,
    PairedSwapSpace,
    StratifiedSpace,
    unrank_combination,
)

__all__ = [
    "ExactAccumulator",
    "ExactResult",
    "IndependentTwoSampleSpace",
    "MonteCarloResult",
    "PairedSwapSpace",
    "StratifiedSpace",
    "accumulate_shard",
    "exact_test",
    "is_extreme",
    "monte_carlo_test",
    "paired_test",
    "shard_ranges",
    "stratified_test",
    "two_sample_test",
    "unrank_combination",
    "wilson_interval",
]
