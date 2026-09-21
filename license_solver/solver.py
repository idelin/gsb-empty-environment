"""Branch-and-bound solver over the whole dependency graph.

OR branches are decided globally here, never per-package, because a branch
choice constrains which transitive dependency versions are admissible.

Objective (lexicographic):
  1. minimize the number of distinct packages that need an exception
     (synthetic rule, e.g. strong copyleft inside a proprietary target);
  2. minimize the number of distinct packages with a disclosure obligation.
Shared dependencies are a single package in the selection, hence counted once.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from itertools import product

from .expression import evaluate, or_nodes
from .model import Build, Repository
from .version import Version

OPTIMAL = "optimal"
FEASIBLE_UPPER_BOUND = "feasible_upper_bound"
INFEASIBLE = "infeasible"
UNKNOWN = "unknown"


@dataclass(frozen=True)
class Selection:
    package: str
    version: Version
    license_text: str
    branches: tuple
    licenses: tuple
    obligations: frozenset
    effective_tags: frozenset
    needs_exception: bool
    propagated_from: tuple


@dataclass
class SolveResult:
    status: str
    selection: dict | None
    cost: tuple | None  # (packages needing exception, packages needing disclosure)
    nodes: int
    budget: int
    conflicts: list = field(default_factory=list)
    message: str = ""

    @property
    def feasible(self) -> bool:
        return self.selection is not None


@dataclass
class EvalOutcome:
    licenses: dict
    obligations: dict
    effective_tags: dict
    needs_exception: dict
    propagated_from: dict
    conflicts: list
    cost: tuple


def evaluate_selection(repo: Repository, build: Build, choices: dict) -> EvalOutcome:
    """Obligations, propagation, conflicts and cost of a (partial) selection.

    ``choices`` maps package name -> (PackageVersion, branch tuple).
    """
    licenses = {}
    for name, (pv, combo) in choices.items():
        lics, _ = evaluate(
            pv.expr, combo, 0, build.attributes, repo.licenses, repo.exceptions
        )
        licenses[name] = tuple(lics)

    obligations = {}
    tags = {}
    for name, lics in licenses.items():
        obligations[name] = frozenset().union(*(l.obligations for l in lics))
        tags[name] = frozenset().union(*(l.tags for l in lics))

    # Propagation fixpoint: copyleft tags travel up the dependency edges
    # whose link type the rule allows, carrying their obligations along.
    eff_tags = dict(tags)
    propagated = {name: set() for name in choices}
    changed = True
    while changed:
        changed = False
        for parent, (pv, _combo) in choices.items():
            for dep_name, spec in pv.deps.items():
                if dep_name not in choices:
                    continue
                link = spec.link or build.link
                for rule in build.rules.propagation:
                    if rule.tag not in eff_tags[dep_name] or link not in rule.links:
                        continue
                    if rule.tag not in eff_tags[parent]:
                        eff_tags[parent] = eff_tags[parent] | {rule.tag}
                        changed = True
                    if not rule.obligations <= obligations[parent]:
                        obligations[parent] = obligations[parent] | rule.obligations
                        changed = True
                    propagated[parent].add(dep_name)

    present = {}
    for name, lics in licenses.items():
        for lic in lics:
            present.setdefault(lic.name, []).append(name)
    conflicts = []
    for pair in build.rules.incompatible:
        if all(lic in present for lic in pair):
            holders = "; ".join(
                f"{lic} (from {', '.join(sorted(present[lic]))})" for lic in pair
            )
            conflicts.append(f"incompatible licenses in target: {holders}")

    needs_exception = {
        name: any(
            rule.tag in eff_tags[name] and rule.matches(build.attributes)
            for rule in build.rules.exception_required
        )
        for name in choices
    }
    cost = (
        sum(needs_exception.values()),
        sum("disclose" in obligations[name] for name in choices),
    )
    return EvalOutcome(
        licenses,
        obligations,
        eff_tags,
        needs_exception,
        {name: tuple(sorted(srcs)) for name, srcs in propagated.items()},
        conflicts,
        cost,
    )


class _BudgetExceeded(Exception):
    pass


class Solver:
    """Depth-first branch and bound with a node budget.

    ``fixed`` maps a package name to ``(version_text, branches_or_None)``
    and pins that package, which is how incremental repair reuses choices.
    """

    def __init__(self, repo: Repository, build: Build, budget: int = 100000, fixed=None):
        self.repo = repo
        self.build = build
        self.budget = budget
        self.fixed = dict(fixed or {})
        self.order = self._compute_order()
        self.nodes = 0
        self.best_choices = None
        self.best_outcome = None
        self.assignment = {}

    def _compute_order(self):
        order, done = [], set()

        def visit(name, active):
            if name in done or name in active or name not in self.repo.packages:
                return
            active.add(name)
            for pv in self.repo.packages[name]:
                for dep in pv.deps:
                    visit(dep, active)
            active.discard(name)
            done.add(name)
            order.append(name)

        for root in self.build.roots:
            visit(root, set())
        order.reverse()  # reverse post-order: parents before children
        return order

    def solve(self) -> SolveResult:
        missing = [r for r in self.build.roots if r not in self.repo.packages]
        if missing:
            return SolveResult(
                INFEASIBLE, None, None, 0, self.budget,
                message=f"missing packages in repository: {missing}",
            )
        self.assignment = {}
        try:
            self._dfs(0)
        except _BudgetExceeded:
            if self.best_choices is not None:
                return self._result(
                    FEASIBLE_UPPER_BOUND,
                    "search budget exhausted; best feasible selection reported as an "
                    "upper bound - optimality undecided, constraints verified only "
                    "for the returned selection, not for the whole search space",
                )
            return SolveResult(
                UNKNOWN, None, None, self.nodes, self.budget,
                message="search budget exhausted before any feasible selection was "
                "found - satisfiability undecided; no claim that all constraints pass",
            )
        if self.best_choices is None:
            return SolveResult(
                INFEASIBLE, None, None, self.nodes, self.budget,
                message="no feasible selection exists (proven by exhaustive search "
                "within budget)",
            )
        return self._result(OPTIMAL, "optimal selection proven within budget")

    def _result(self, status, message) -> SolveResult:
        return SolveResult(
            status,
            self._build_selection(),
            self.best_outcome.cost,
            self.nodes,
            self.budget,
            list(self.best_outcome.conflicts),
            message,
        )

    def _dfs(self, index):
        self.nodes += 1
        if self.nodes > self.budget:
            raise _BudgetExceeded
        if index == len(self.order):
            if not self._deps_consistent():
                return
            outcome = evaluate_selection(self.repo, self.build, self.assignment)
            if outcome.conflicts:
                return
            if self.best_outcome is None or outcome.cost < self.best_outcome.cost:
                self.best_outcome = outcome
                self.best_choices = dict(self.assignment)
            return
        name = self.order[index]
        if not self._is_needed(name):
            self._dfs(index + 1)
            return
        for pv, combo in self._candidates(name):
            self.assignment[name] = (pv, combo)
            outcome = evaluate_selection(self.repo, self.build, self.assignment)
            # Partial costs only grow as packages are added, so a partial
            # cost that cannot beat the incumbent prunes the subtree.
            if not outcome.conflicts and (
                self.best_outcome is None or outcome.cost < self.best_outcome.cost
            ):
                self._dfs(index + 1)
            del self.assignment[name]

    def _is_needed(self, name):
        if name in self.build.roots:
            return True
        return any(name in pv.deps for pv, _ in self.assignment.values())

    def _deps_consistent(self):
        for _name, (pv, _combo) in self.assignment.items():
            for dep, spec in pv.deps.items():
                if dep not in self.assignment:
                    return False
                if not spec.range.contains(self.assignment[dep][0].version):
                    return False
        return True

    def _candidates(self, name):
        ranges = []
        if name in self.build.roots:
            ranges.append(self.build.roots[name])
        for pv_parent, _ in self.assignment.values():
            spec = pv_parent.deps.get(name)
            if spec is not None:
                ranges.append(spec.range)
        fixed = self.fixed.get(name)
        out = []
        for pv in sorted(self.repo.packages[name], key=lambda p: p.version, reverse=True):
            if fixed is not None and str(pv.version) != fixed[0]:
                continue
            if not all(r.contains(pv.version) for r in ranges):
                continue
            if any(dep not in self.repo.packages for dep in pv.deps):
                continue
            n_or = len(or_nodes(pv.expr))
            combos = list(product((0, 1), repeat=n_or)) if n_or else [()]
            if fixed is not None and fixed[1] is not None:
                combos = [tuple(fixed[1])] if len(fixed[1]) == n_or else []
            combos.sort(key=lambda c: self._local_cost(pv, c))
            out.extend((pv, combo) for combo in combos)
        return out

    def _local_cost(self, pv, combo):
        lics, _ = evaluate(
            pv.expr, combo, 0, self.build.attributes,
            self.repo.licenses, self.repo.exceptions,
        )
        return sum(len(l.obligations) + 2 * len(l.tags) for l in lics)

    def _build_selection(self):
        outcome = self.best_outcome
        selection = {}
        for name, (pv, combo) in self.best_choices.items():
            selection[name] = Selection(
                package=name,
                version=pv.version,
                license_text=pv.license_text,
                branches=combo,
                licenses=outcome.licenses[name],
                obligations=outcome.obligations[name],
                effective_tags=outcome.effective_tags[name],
                needs_exception=outcome.needs_exception[name],
                propagated_from=outcome.propagated_from[name],
            )
        return selection
