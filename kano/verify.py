"""Independent verification of a search result.

Recomputes equivalence classes from the raw records and the claimed level
vector, then checks the k-constraint and the reported class-size multiset.
"""

from __future__ import annotations

from .hierarchy import GeneralizationHierarchy
from .search import SearchResult, equivalence_class_sizes, is_k_anonymous


def verify_result(
    records: list[tuple[str, ...]],
    hierarchies: list[GeneralizationHierarchy],
    result: SearchResult,
    k: int,
) -> bool:
    if not result.solved:
        return result.levels is None and result.equivalence_class_sizes is None
    assert result.levels is not None
    sizes = equivalence_class_sizes(records, hierarchies, result.levels)
    return (
        sizes == result.equivalence_class_sizes
        and is_k_anonymous(sizes, k)
    )
