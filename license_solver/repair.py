"""Incremental repair after a version change, with conflict-core output.

Workflow: keep the selections of packages outside the changed package's
reverse-dependency cone pinned, re-solve only the cone. If that is
infeasible, compute a deletion-minimal conflict core over the carried-over
pins, then fall back to a full re-solve.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .solver import INFEASIBLE, Solver


@dataclass
class RepairResult:
    status: str
    strategy: str  # "local" | "full" | "none"
    selection: dict | None
    cost: tuple | None
    conflict_core: list = field(default_factory=list)
    message: str = ""

    @property
    def feasible(self) -> bool:
        return self.selection is not None


def _reverse_cone(repo, previous: dict, changed: set) -> set:
    """Packages that transitively depend on a changed package, following the
    dependency edges of the previously selected versions."""
    deps_of = {}
    for name, (version_text, _branches) in previous.items():
        pv = repo.find(name, version_text)
        deps_of[name] = list(pv.deps) if pv else []
    cone = set(changed)
    frontier = list(changed)
    while frontier:
        target = frontier.pop()
        for parent, deps in deps_of.items():
            if target in deps and parent not in cone:
                cone.add(parent)
                frontier.append(parent)
    return cone


def repair(repo, build, previous: dict, forced: dict, budget: int = 20000,
           check_budget: int = 5000) -> RepairResult:
    """Re-solve after forced version changes, reusing previous selections.

    ``previous``: {package: (version_text, branches)} from an earlier solve.
    ``forced``:   {package: version_text} non-negotiable version changes.
    """
    cone = _reverse_cone(repo, previous, set(forced))
    pins = {name: value for name, value in previous.items() if name not in cone}
    fixed = dict(pins)
    fixed.update({name: (text, None) for name, text in forced.items()})

    local = Solver(repo, build, budget=budget, fixed=fixed).solve()
    if local.feasible:
        return RepairResult(
            local.status,
            "local",
            local.selection,
            local.cost,
            [],
            f"reused {len(pins)} pinned selection(s); re-solved only the affected "
            f"cone {sorted(cone)}; {local.message}",
        )

    core = _conflict_core(repo, build, pins, forced, check_budget)
    full = Solver(
        repo, build, budget=budget,
        fixed={n: (v, None) for n, v in forced.items()},
    ).solve()
    strategy = "full" if full.feasible else "none"
    return RepairResult(
        full.status,
        strategy,
        full.selection,
        full.cost,
        core,
        f"local repair infeasible; conflict core: {core}; "
        f"full re-solve: {full.message}",
    )


def _conflict_core(repo, build, pins, forced, budget) -> list:
    """Deletion-minimal set of carried-over pins conflicting with the changes."""

    def infeasible(keep) -> bool:
        fixed = {n: pins[n] for n in keep}
        fixed.update({n: (v, None) for n, v in forced.items()})
        # A budget-undecided check is treated as "not proven infeasible",
        # so the pin stays in the core (conservative).
        return Solver(repo, build, budget=budget, fixed=fixed).solve().status == INFEASIBLE

    core = set(pins)
    for name in list(core):
        if infeasible(core - {name}):
            core.discard(name)
    labels = [f"forced {n}@{v}" for n, v in sorted(forced.items())]
    labels += [f"pin {n}@{pins[n][0]}" for n in sorted(core)]
    return labels
