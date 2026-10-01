"""Sampling-model assumptions under which the given formula is valid.

The formula  eps' = log(1 + q*(exp(eps) - 1))  is the standard
subsampling-amplification bound for **Poisson sampling** with
**add/remove adjacency** and an (eps, 0)-DP base mechanism.  If the input
does not satisfy this model (e.g. sampling without replacement, a
different adjacency, delta > 0, inconsistent datasets across rounds),
applying the formula is unsound and this module *rejects* the
combination instead of producing a misleadingly precise number.
"""

from __future__ import annotations

from dataclasses import dataclass


class AssumptionError(ValueError):
    """Raised when inputs do not satisfy the required sampling model."""


@dataclass(frozen=True)
class SamplingAssumption:
    """Declarative record of the sampling model behind a parameter pair.

    scheme:        only "poisson" is accepted for the given formula
    adjacency:     only "add_remove" is accepted for the given formula
    base_delta:    delta of the base (eps, delta)-DP mechanism; the given
                   formula is the pure-DP bound, so delta must be 0
    dataset_size:  optional; rounds composed together must agree on it
    """

    scheme: str = "poisson"
    adjacency: str = "add_remove"
    base_delta: float = 0.0
    dataset_size: int | None = None

    def check_formula_applicable(self) -> None:
        if self.scheme != "poisson":
            raise AssumptionError(
                f"sampling scheme {self.scheme!r}: the formula "
                "eps'=log(1+q*(exp(eps)-1)) is the Poisson-subsampling "
                "bound; refusing to apply it"
            )
        if self.adjacency != "add_remove":
            raise AssumptionError(
                f"adjacency {self.adjacency!r}: the given formula assumes "
                "add/remove adjacency; refusing to apply it"
            )
        if self.base_delta != 0.0:
            raise AssumptionError(
                f"base_delta={self.base_delta!r}: the given formula is the "
                "pure-DP (delta=0) amplification bound; refusing to apply it"
            )
        if self.dataset_size is not None and self.dataset_size <= 0:
            raise AssumptionError("dataset_size must be a positive integer")


def check_composable(assumptions: list[SamplingAssumption]) -> None:
    """Verify that rounds may be composed under one shared sampling model."""
    if not assumptions:
        raise AssumptionError("nothing to compose")
    for a in assumptions:
        a.check_formula_applicable()
    first = assumptions[0]
    for a in assumptions[1:]:
        if a != first:
            raise AssumptionError(
                "inconsistent sampling models across rounds "
                f"({first!r} vs {a!r}); refusing to compose"
            )
