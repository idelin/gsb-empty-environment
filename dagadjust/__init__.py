"""Exact weighted least-squares adjustment of counts on a counting DAG.

Synthetic data only.  Statistical experiments with this package are not
a formal privacy proof of any kind.
"""

from .certificates import Certificates
from .problem import AdjustmentProblem, CountDAG
from .solver import (
    STATUS_INFEASIBLE,
    STATUS_NOT_CONVERGED,
    STATUS_OPTIMAL,
    STATUS_SINGULAR,
    SolveResult,
    solve,
)

__all__ = [
    "AdjustmentProblem",
    "Certificates",
    "CountDAG",
    "SolveResult",
    "STATUS_INFEASIBLE",
    "STATUS_NOT_CONVERGED",
    "STATUS_OPTIMAL",
    "STATUS_SINGULAR",
    "solve",
]
