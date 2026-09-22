"""Incremental repair after a package version change.

Workflow: solve once, keep the solution; then the local index changes
(e.g. a new version of a package appears, or a pin overrides the
resolved version). ``repair`` reuses the previous choices:

1. Choices for packages whose license terms are unchanged are pinned
   (fixed); only genuinely free variables are re-searched.
2. If the pinned subproblem is infeasible, all pins are released and a
   warm-started full re-solve runs (previous choices tried first).
3. If the whole problem is infeasible, a conflict core is computed so
   the caller sees exactly which rules/packages cannot be reconciled.
"""

from __future__ import annotations

from dataclasses import dataclass

from .core import CoreResult, conflict_core
from .model import Problem
from .solver import (
    STATUS_INFEASIBLE,
    FixedChoiceError,
    SolveResult,
    Solver,
)


@dataclass
class RepairResult:
    status: str
    solve: SolveResult | None
    kept: tuple[str, ...]      # previous choices reused unchanged
    changed: tuple[str, ...]   # packages whose choice differs from before
    dropped: tuple[str, ...]   # previous packages no longer selectable
    mode: str                  # "pinned" | "resolved"
    core: CoreResult | None

    def as_json(self) -> dict:
        out = {
            "status": self.status,
            "mode": self.mode,
            "kept": list(self.kept),
            "changed": list(self.changed),
            "dropped": list(self.dropped),
        }
        if self.solve is not None:
            out["objective"] = (
                {"exceptions": self.solve.objective[0],
                 "disclosures": self.solve.objective[1]}
                if self.solve.objective else None
            )
            out["choices"] = self.solve.choices_as_json()
            out["complete"] = self.solve.complete
        if self.core is not None:
            out["conflict_core"] = self.core.as_json()
        return out


def _term_key(term_dicts: list[dict]) -> tuple[tuple[str, str | None], ...]:
    return tuple(sorted((d["license"], d.get("exception")) for d in term_dicts))


def _match_term_index(pkg, term_dicts: list[dict]) -> int | None:
    want = _term_key(term_dicts)
    for idx, term in enumerate(pkg.terms):
        have = tuple(sorted((a.license, a.exception) for a in term))
        if have == want:
            return idx
    return None


def repair(
    problem: Problem,
    previous_choices: dict[str, list[dict]],
    budget: int = 100_000,
    core_budget: int = 200_000,
) -> RepairResult:
    solver = Solver(problem, budget=budget)
    fixed: dict[str, int] = {}
    dropped: list[str] = []
    for ref, term_dicts in previous_choices.items():
        pkg = problem.graph.nodes.get(ref)
        if pkg is None:
            dropped.append(ref)
            continue
        idx = _match_term_index(pkg, term_dicts)
        if idx is None:
            dropped.append(ref)  # license terms changed with the version
        else:
            fixed[ref] = idx

    # Phase 1: keep every still-valid previous choice pinned.
    pinned: SolveResult | None = None
    try:
        pinned = solver.solve(fixed=fixed, preferred=fixed)
    except FixedChoiceError:
        pinned = None
    if pinned is not None and pinned.objective is not None:
        kept = tuple(sorted(r for r in fixed if r in pinned.choices))
        changed = tuple(sorted(r for r in pinned.choices if r not in fixed))
        return RepairResult(
            pinned.status, pinned, kept, changed, tuple(sorted(dropped)),
            "pinned", None,
        )

    # Phase 2: release pins, warm-start from previous choices.
    full = solver.solve(preferred=fixed)
    if full.status == STATUS_INFEASIBLE:
        core = conflict_core(problem, budget=core_budget)
        return RepairResult(
            full.status, full, (), (), tuple(sorted(dropped)), "resolved", core,
        )
    changed = tuple(
        sorted(
            ref
            for ref, term in full.choices.items()
            if ref not in previous_choices
            or _term_key(previous_choices[ref])
            != tuple(sorted((a.license, a.exception) for a in term))
        )
    )
    kept = tuple(sorted(set(full.choices) - set(changed)))
    return RepairResult(
        full.status, full, kept, changed, tuple(sorted(dropped)), "resolved", None,
    )
