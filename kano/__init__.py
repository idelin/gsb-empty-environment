"""k-anonymity via full-domain generalization on synthetic data."""

from .hierarchy import GeneralizationHierarchy
from .loss import LossModel
from .search import SearchResult, Status, brute_force, equivalence_class_sizes, search
from .verify import verify_result

__all__ = [
    "GeneralizationHierarchy",
    "LossModel",
    "SearchResult",
    "Status",
    "brute_force",
    "equivalence_class_sizes",
    "search",
    "verify_result",
]
