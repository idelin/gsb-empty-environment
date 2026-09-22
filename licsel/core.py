"""Conflict-core extraction for infeasible problems.

The core is a deletion-minimal set of rule instances (rule id, trigger
package, scope packages) that is already unsatisfiable on its own:
no choice of license branches for the involved packages can satisfy
all of them simultaneously. Minimality is proven only when every
sub-check finished within the budget; otherwise ``minimal=False``.
"""

from __future__ import annotations

from dataclasses import dataclass
from itertools import product

from .model import ConstraintInstance, Problem, compute_instances
from .solver import instance_holds


@dataclass
class CoreResult:
    instances: tuple[ConstraintInstance, ...]
    involved_packages: tuple[str, ...]
    minimal: bool  # False when the budget prevented proving minimality

    def as_json(self) -> dict:
        return {
            "minimal": self.minimal,
            "involved_packages": list(self.involved_packages),
            "instances": [
                {
                    "rule_id": inst.rule_id,
                    "trigger": inst.trigger,
                    "scope": sorted(inst.scope_nodes),
                    "message": inst.message,
                }
                for inst in self.instances
            ],
        }


def _satisfiable(
    problem: Problem,
    instances: list[ConstraintInstance],
    budget_counter: dict,
) -> bool | None:
    """True/False, or None when the shared budget ran out (unknown)."""
    nodes = sorted(set().union(*(inst.involved for inst in instances)))
    terms_per_node = [problem.graph.nodes[ref].terms for ref in nodes]
    for combo in product(*terms_per_node):
        budget_counter["left"] -= 1
        if budget_counter["left"] < 0:
            return None
        assignment = dict(zip(nodes, combo))
        if all(instance_holds(problem, inst, assignment) for inst in instances):
            return True
    return False


def conflict_core(problem: Problem, budget: int = 200_000) -> CoreResult:
    instances = compute_instances(problem)
    counter = {"left": budget}
    minimal = True

    if not instances:
        return CoreResult((), (), True)
    if _satisfiable(problem, instances, counter):
        # Problem is actually feasible; no conflict core exists.
        return CoreResult((), (), True)

    core = list(instances)
    for inst in list(core):
        trial = [c for c in core if c is not inst]
        if not trial:
            break
        sat = _satisfiable(problem, trial, counter)
        if sat is False:
            core = trial
        elif sat is None:
            minimal = False  # budget exhausted: keep inst, minimality unproven
    involved = sorted(set().union(*(inst.involved for inst in core)))
    return CoreResult(tuple(core), tuple(involved), minimal)
