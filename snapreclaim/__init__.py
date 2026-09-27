"""Multi-snapshot dedup space reclamation planner.

Content blocks are shared by file versions; snapshots reference file
versions.  A block is reclaimable only when every snapshot that
(transitively) references it has been deleted.  This package selects a
set of snapshots to delete that satisfies a reclamation requirement with
minimum business loss, considering the complementarity of shared blocks.
"""

from .model import Problem, Snapshot, compute_reclaim, load_problem
from .solver import SearchResult, solve_max_reclaim, solve_min_loss

__all__ = [
    "Problem",
    "Snapshot",
    "SearchResult",
    "compute_reclaim",
    "load_problem",
    "solve_min_loss",
    "solve_max_reclaim",
]
