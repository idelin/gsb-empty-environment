"""Region algebra over finite enumerations and closed integer intervals.

A *region* is a dict mapping fact id -> constraint, where a constraint is
either an ``EnumSet`` (finite set of allowed values) or an ``IntRange``
(closed interval [lo, hi]).  A missing key means "the whole domain of that
fact", so the domain itself must be supplied by the caller when needed.
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class EnumSet:
    values: frozenset

    def __init__(self, values):
        object.__setattr__(self, "values", frozenset(values))
        if not self.values:
            raise ValueError("EnumSet must not be empty")


@dataclass(frozen=True)
class IntRange:
    lo: int
    hi: int

    def __post_init__(self):
        if self.lo > self.hi:
            raise ValueError(f"empty IntRange [{self.lo}, {self.hi}]")


def intersect_constraints(a, b):
    """Return the intersection of two constraints, or None if disjoint."""
    if isinstance(a, EnumSet) and isinstance(b, EnumSet):
        common = a.values & b.values
        return EnumSet(common) if common else None
    if isinstance(a, IntRange) and isinstance(b, IntRange):
        lo, hi = max(a.lo, b.lo), min(a.hi, b.hi)
        return IntRange(lo, hi) if lo <= hi else None
    raise TypeError(f"constraint kind mismatch: {a!r} vs {b!r}")


def subtract_constraint(a, b):
    """Return a list of constraints covering ``a \\ b`` (0, 1 or 2 pieces)."""
    if isinstance(a, EnumSet) and isinstance(b, EnumSet):
        diff = a.values - b.values
        return [EnumSet(diff)] if diff else []
    if isinstance(a, IntRange) and isinstance(b, IntRange):
        pieces = []
        if a.lo <= min(a.hi, b.lo - 1):
            pieces.append(IntRange(a.lo, min(a.hi, b.lo - 1)))
        if max(a.lo, b.hi + 1) <= a.hi:
            pieces.append(IntRange(max(a.lo, b.hi + 1), a.hi))
        return pieces
    raise TypeError(f"constraint kind mismatch: {a!r} vs {b!r}")


def is_disjoint(region_a, region_b):
    """Two regions are disjoint iff they disagree on some shared fact."""
    for fact_id in region_a.keys() & region_b.keys():
        if intersect_constraints(region_a[fact_id], region_b[fact_id]) is None:
            return True
    return False


def intersect_regions(region_a, region_b):
    """Return the intersection region, or None if the regions are disjoint."""
    merged = {}
    for fact_id in region_a.keys() | region_b.keys():
        ca, cb = region_a.get(fact_id), region_b.get(fact_id)
        if ca is None:
            merged[fact_id] = cb
        elif cb is None:
            merged[fact_id] = ca
        else:
            both = intersect_constraints(ca, cb)
            if both is None:
                return None
            merged[fact_id] = both
    return merged


def subtract_region(region, box, domain):
    """Return a list of disjoint regions covering ``region \\ box``.

    ``domain`` maps every fact id to its full-domain constraint; it is needed
    because a fact unconstrained in ``region`` still has to be split when
    ``box`` constrains that fact.
    """
    fragments = []
    current = dict(region)
    for fact_id, box_constraint in box.items():
        current_constraint = current.get(fact_id, domain[fact_id])
        for outside in subtract_constraint(current_constraint, box_constraint):
            fragment = dict(current)
            fragment[fact_id] = outside
            fragments.append(fragment)
        inside = intersect_constraints(current_constraint, box_constraint)
        if inside is None:
            # Remainder of `current` is disjoint from `box`; nothing more
            # can be subtracted from it.
            return fragments
        current[fact_id] = inside
    return fragments


def constraint_to_json(constraint):
    if isinstance(constraint, EnumSet):
        return {"enum": sorted(constraint.values)}
    return {"int": [constraint.lo, constraint.hi]}


def region_to_json(region):
    """Canonical JSON form of a region (keys sorted)."""
    return {k: constraint_to_json(region[k]) for k in sorted(region)}
