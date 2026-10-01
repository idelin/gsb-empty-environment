"""Core data model for the budgeted maximum-coverage problem.

All numeric data (costs, weights, budget) are synthetic integers or
rationals and are stored exactly as ``fractions.Fraction``.  No external
solver is used anywhere in this package.

Problem statement
-----------------
Choose a collection of sets whose total cost does not exceed the budget so
that the total *profit* is maximised.  An element yields its weight exactly
once (repeated coverage never counts twice) and only when it is covered at
least ``min_cover[element]`` times (default 1).  Selecting a set forces its
``requires`` closure to be selected as well; sets listed in ``excludes``
are mutually exclusive (checked in both directions).
"""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass
from enum import Enum
from fractions import Fraction
from typing import Dict, FrozenSet, Iterable, List, Set, Tuple


class Status(Enum):
    """Outcome categories; feasible / optimal / budget-undecided stay separate."""

    OPTIMAL = "optimal"                    # proven optimal by exact search
    FEASIBLE = "feasible"                  # feasible solution, no optimality claim
    BUDGET_UNDECIDED = "budget_undecided"  # search budget exhausted; gap undecided


class UnsatisfiableDependency(Exception):
    """Raised when a set requires a set that does not exist in the instance."""

    def __init__(self, missing: str):
        super().__init__(missing)
        self.missing = missing


@dataclass(frozen=True)
class CoverageSet:
    name: str
    cost: Fraction
    elements: FrozenSet[str]
    requires: FrozenSet[str]
    excludes: FrozenSet[str]

    def __init__(self, name, cost=0, elements=(), requires=(), excludes=()):
        cost = Fraction(cost)
        if cost < 0:
            raise ValueError(f"set {name!r} has negative cost {cost}")
        object.__setattr__(self, "name", str(name))
        object.__setattr__(self, "cost", cost)
        object.__setattr__(self, "elements", frozenset(str(e) for e in elements))
        object.__setattr__(self, "requires", frozenset(str(r) for r in requires))
        object.__setattr__(self, "excludes", frozenset(str(x) for x in excludes))


@dataclass
class Evaluation:
    feasible: bool
    profit: Fraction
    cost: Fraction
    violations: List[str]
    counts: Dict[str, int]


class Instance:
    """A budgeted coverage instance with dependencies, conflicts and min-cover."""

    def __init__(self, sets, weights, budget, min_cover=None):
        sets = list(sets)
        names = [s.name for s in sets]
        if len(set(names)) != len(names):
            raise ValueError("duplicate set names")
        self.sets: Dict[str, CoverageSet] = {s.name: s for s in sets}
        self.weights: Dict[str, Fraction] = {
            str(e): Fraction(w) for e, w in dict(weights).items()
        }
        for e, w in self.weights.items():
            if w < 0:
                raise ValueError(f"element {e!r} has negative weight {w}")
        self.budget = Fraction(budget)
        if self.budget < 0:
            raise ValueError("negative budget")
        self.min_cover: Dict[str, int] = {}
        for e, k in dict(min_cover or {}).items():
            k = int(k)
            if k < 1:
                raise ValueError(f"min_cover for {e!r} must be >= 1")
            self.min_cover[str(e)] = k
        for s in sets:
            for e in s.elements:
                self.weights.setdefault(e, Fraction(0))

    def required_cover(self, element: str) -> int:
        return self.min_cover.get(element, 1)

    def total_cost(self, selected: Iterable[str]) -> Fraction:
        return sum((self.sets[n].cost for n in selected), Fraction(0))

    def coverage_counts(self, selected: Iterable[str]) -> Dict[str, int]:
        counts: Dict[str, int] = {}
        for n in selected:
            for e in self.sets[n].elements:
                counts[e] = counts.get(e, 0) + 1
        return counts

    def profit_from_counts(self, counts: Dict[str, int]) -> Fraction:
        """Each element contributes its weight exactly once when covered enough."""
        total = Fraction(0)
        for e, w in self.weights.items():
            if w and counts.get(e, 0) >= self.required_cover(e):
                total += w
        return total

    def profit_of(self, selected: Iterable[str]) -> Fraction:
        return self.profit_from_counts(self.coverage_counts(selected))

    def marginal_gain(self, counts: Dict[str, int], added: Iterable[str]) -> Fraction:
        """Exact profit gained by adding ``added`` sets to coverage ``counts``."""
        simulated = dict(counts)
        gain = Fraction(0)
        for n in added:
            for e in self.sets[n].elements:
                before = simulated.get(e, 0)
                after = before + 1
                simulated[e] = after
                req = self.required_cover(e)
                if before < req <= after:
                    gain += self.weights.get(e, Fraction(0))
        return gain

    def optimistic_marginal(self, counts: Dict[str, int], elements: Iterable[str]) -> Fraction:
        """Overestimate of marginal gain (ignores min-cover thresholds).

        Used only inside upper-bound computations, where overestimating is
        safe; never used to compute actual profit.
        """
        total = Fraction(0)
        for e in elements:
            if counts.get(e, 0) < self.required_cover(e):
                total += self.weights.get(e, Fraction(0))
        return total

    def dependency_closure(self, names: Iterable[str]) -> Set[str]:
        closure: Set[str] = set()
        stack = list(names)
        while stack:
            n = stack.pop()
            if n in closure:
                continue
            s = self.sets.get(n)
            if s is None:
                raise UnsatisfiableDependency(n)
            closure.add(n)
            stack.extend(s.requires)
        return closure

    def conflicting_pairs(self, names: Iterable[str]) -> List[Tuple[str, str]]:
        names = set(names)
        pairs = set()
        for a in names:
            for b in self.sets[a].excludes:
                if b in names and a != b:
                    pairs.add(tuple(sorted((a, b))))
        for a in names:
            for b in names:
                if a < b and a in self.sets[b].excludes:
                    pairs.add((a, b))
        return sorted(pairs)

    def evaluate(self, selected: Iterable[str]) -> Evaluation:
        selected = set(selected)
        violations: List[str] = []
        unknown = sorted(n for n in selected if n not in self.sets)
        if unknown:
            violations.append(f"unknown sets selected: {unknown}")
            return Evaluation(False, Fraction(0), Fraction(0), violations, {})
        try:
            closure = self.dependency_closure(selected)
        except UnsatisfiableDependency as exc:
            violations.append(f"unsatisfiable dependency: {exc.missing!r} does not exist")
            closure = set(selected)
        missing = sorted(closure - selected)
        if missing:
            violations.append(f"missing required sets: {missing}")
        for a, b in self.conflicting_pairs(selected):
            violations.append(f"mutually exclusive sets both selected: {a!r} and {b!r}")
        cost = self.total_cost(selected)
        if cost > self.budget:
            violations.append(f"cost {cost} exceeds budget {self.budget}")
        counts = self.coverage_counts(selected)
        profit = self.profit_from_counts(counts)
        return Evaluation(not violations, profit, cost, violations, counts)

    def remove_set(self, name: str) -> "Instance":
        """Return a new instance without ``name``.

        ``excludes`` references to the removed set are dropped (vacuous);
        ``requires`` references are kept, so any set depending on the removed
        set becomes unselectable.  Any previously computed witness must be
        re-validated against the new instance (see ``witness.verify``).
        """
        if name not in self.sets:
            raise KeyError(name)
        new_sets = []
        for s in self.sets.values():
            if s.name == name:
                continue
            new_sets.append(
                CoverageSet(
                    s.name,
                    s.cost,
                    s.elements,
                    requires=s.requires,
                    excludes=s.excludes - {name},
                )
            )
        return Instance(new_sets, self.weights, self.budget, self.min_cover)

    def fingerprint(self) -> str:
        canon = {
            "sets": sorted(
                [
                    s.name,
                    str(s.cost),
                    sorted(s.elements),
                    sorted(s.requires),
                    sorted(s.excludes),
                ]
                for s in self.sets.values()
            ),
            "weights": sorted((e, str(w)) for e, w in self.weights.items()),
            "min_cover": sorted(self.min_cover.items()),
            "budget": str(self.budget),
        }
        blob = json.dumps(canon, sort_keys=True).encode()
        return hashlib.sha256(blob).hexdigest()[:16]
