"""Exact and empirical absorption analysis of finite Markov chains."""

from .chain import Chain
from .exact import (absorption_probabilities, expected_hitting_times,
                    conditional_expected_time, absorption_residuals,
                    hitting_time_residuals, probability_sum_witness,
                    solve_linear)
from .lumping import (check_strong_lumpability, lumped_chain,
                      LumpabilityWitness)
from .simulate import sample_path, estimate_absorption, EmpiricalResult
from .revision import revise_row, RevisionReport

__all__ = [
    "Chain", "absorption_probabilities", "expected_hitting_times",
    "conditional_expected_time", "absorption_residuals",
    "hitting_time_residuals", "probability_sum_witness", "solve_linear",
    "check_strong_lumpability", "lumped_chain", "LumpabilityWitness",
    "sample_path", "estimate_absorption", "EmpiricalResult",
    "revise_row", "RevisionReport",
]
