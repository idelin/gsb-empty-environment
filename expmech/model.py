"""Data model and constraint handling for the subset exponential mechanism.

Synthetic data only. All definitions are stated in README.md; nothing here
constitutes a formal privacy proof.
"""

from __future__ import annotations

import itertools
from dataclasses import dataclass, field
from typing import Mapping, Tuple, FrozenSet


class InfeasibleError(ValueError):
    """Raised when the constraint set admits no legal subset.

    The mechanism never silently drops candidates or shrinks the requested
    subset size to regain feasibility: an insufficient selection budget is
    always reported as an error instead of sampling from a truncated,
    different distribution while claiming the original one.
    """


@dataclass(frozen=True)
class Candidate:
    """A single synthetic candidate.

    cid:   unique identifier (duplicate ids are rejected).
    score: finite integer utility score.
    group: group label used by per-group quotas.
    """

    cid: str
    score: int
    group: str = "default"

    def __post_init__(self) -> None:
        if not isinstance(self.cid, str) or not self.cid:
            raise ValueError("candidate id must be a non-empty string")
        # bool is a subclass of int; exclude it explicitly.
        if isinstance(self.score, bool) or not isinstance(self.score, int):
            raise ValueError(f"score of {self.cid!r} must be an integer")
        if not isinstance(self.group, str) or not self.group:
            raise ValueError(f"group of {self.cid!r} must be a non-empty string")


@dataclass(frozen=True)
class Constraints:
    """Feasibility constraints defining the legal-subset domain.

    subset_size:  exact number of candidates per legal subset (the selection
                  budget). A legal subset has *exactly* this many members.
    group_quotas: mapping group -> maximum number of members from that group
                  allowed in any legal subset.
    mutex_pairs:  pairs of candidate ids that may never co-occur.
    """

    subset_size: int
    group_quotas: Mapping[str, int] = field(default_factory=dict)
    mutex_pairs: FrozenSet[FrozenSet[str]] = frozenset()

    def __post_init__(self) -> None:
        if isinstance(self.subset_size, bool) or not isinstance(self.subset_size, int):
            raise ValueError("subset_size must be an integer")
        if self.subset_size < 0:
            raise ValueError("subset_size must be >= 0")
        for group, quota in self.group_quotas.items():
            if isinstance(quota, bool) or not isinstance(quota, int) or quota < 0:
                raise ValueError(f"quota for group {group!r} must be an integer >= 0")
        for pair in self.mutex_pairs:
            if len(pair) != 2:
                raise ValueError("each mutex pair must contain exactly two distinct ids")


def validate_candidates(candidates: Tuple[Candidate, ...]) -> None:
    ids = [c.cid for c in candidates]
    if len(set(ids)) != len(ids):
        duplicates = sorted({i for i in ids if ids.count(i) > 1})
        raise ValueError(f"duplicate candidate ids rejected: {duplicates}")


def check_constraints_refer_to_candidates(
    candidates: Tuple[Candidate, ...], constraints: Constraints
) -> None:
    known = {c.cid for c in candidates}
    for pair in constraints.mutex_pairs:
        unknown = pair - known
        if unknown:
            raise ValueError(f"mutex pair references unknown candidate ids: {sorted(unknown)}")


def is_legal(subset: Tuple[Candidate, ...], constraints: Constraints) -> bool:
    if len(subset) != constraints.subset_size:
        return False
    counts: dict[str, int] = {}
    for c in subset:
        counts[c.group] = counts.get(c.group, 0) + 1
    for group, count in counts.items():
        quota = constraints.group_quotas.get(group)
        if quota is not None and count > quota:
            return False
    ids = {c.cid for c in subset}
    for pair in constraints.mutex_pairs:
        if pair <= ids:
            return False
    return True


def enumerate_legal_subsets(
    candidates: Tuple[Candidate, ...],
    constraints: Constraints,
    max_enumerated: int = 2_000_000,
) -> Tuple[Tuple[Candidate, ...], ...]:
    """Enumerate every legal subset exactly; never truncate the domain.

    Raises InfeasibleError if no legal subset exists (e.g. the selection
    budget exceeds what quotas/mutex rules allow, or all candidates are
    ruled out). Raises OverflowError if the domain is too large to
    enumerate honestly within ``max_enumerated``.
    """
    validate_candidates(candidates)
    check_constraints_refer_to_candidates(candidates, constraints)
    legal = []
    for combo in itertools.combinations(candidates, constraints.subset_size):
        if is_legal(combo, constraints):
            legal.append(combo)
            if len(legal) > max_enumerated:
                raise OverflowError(
                    f"legal-subset domain exceeds max_enumerated={max_enumerated}; "
                    "refusing to truncate the domain"
                )
    if not legal:
        raise InfeasibleError(
            "no legal subset satisfies the constraints "
            f"(subset_size={constraints.subset_size}, "
            f"quotas={dict(constraints.group_quotas)}, "
            f"mutex_pairs={len(constraints.mutex_pairs)}); "
            "the distribution is undefined and no candidate was dropped"
        )
    return tuple(legal)


def utility(subset: Tuple[Candidate, ...]) -> int:
    """Subset utility: exact integer sum of member scores."""
    return sum(c.score for c in subset)


def utility_sensitivity_fixed_size(candidates: Tuple[Candidate, ...]) -> int:
    """Global sensitivity of the sum-utility for fixed-size subsets.

    Replacing one candidate in a size-k subset changes the sum by at most
    max(score) - min(score), so that range is a valid sensitivity.
    """
    if not candidates:
        return 0
    scores = [c.score for c in candidates]
    return max(scores) - min(scores)
