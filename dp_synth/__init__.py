"""Synthetic-data differential-privacy query engine (stdlib only)."""

from .budget import BudgetExceededError, BudgetLedger
from .engine import DPQueryEngine, Publication
from .experiments import MechanismDiagnostics, diagnose_laplace
from .mechanism import Rng, laplace_mechanism, noisy_histogram
from .queries import (
    Condition,
    CountQuery,
    HistogramQuery,
    Predicate,
    SumQuery,
    canonical_query_id,
    true_answer,
)
from .synthetic_data import SyntheticData, make_synthetic_employee_data

__all__ = [
    "BudgetExceededError",
    "BudgetLedger",
    "Condition",
    "CountQuery",
    "DPQueryEngine",
    "HistogramQuery",
    "MechanismDiagnostics",
    "Predicate",
    "Publication",
    "Rng",
    "SumQuery",
    "SyntheticData",
    "canonical_query_id",
    "diagnose_laplace",
    "laplace_mechanism",
    "make_synthetic_employee_data",
    "noisy_histogram",
    "true_answer",
]
