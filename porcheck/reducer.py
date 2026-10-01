"""Ample-set (persistent-set) selection with a cycle proviso, plus evidence.

Reduction strategy (sound for deadlocks and for the declared invariants):

* C1 persistence: a singleton {a} is chosen only when `a` is enabled and
  statically independent of every other action of the model, so any sequence
  of other actions commutes with `a` and cannot disable it.
* C2 invisibility: `a` must not write any variable read by an invariant,
  so the reduced graph cannot hide a change of the property's truth value.
* C3 cycle proviso: if the successor via the singleton is already on the DFS
  stack, the state is fully expanded instead.  This prevents an action from
  being ignored forever along a cycle.

Every decision is recorded as a ReductionDecision for later inspection.
"""

from __future__ import annotations

from dataclasses import dataclass

from .independence import independent_of_all
from .model import Action, Model, State, format_state


@dataclass
class ReductionDecision:
    state: State
    enabled: list[str]
    chosen: list[str]
    reason: str
    candidate: str | None
    proviso_triggered: bool

    @property
    def reduced(self) -> bool:
        return len(self.chosen) < len(self.enabled)

    def __str__(self) -> str:
        return (
            f"state {format_state(self.state)}: enabled={self.enabled} "
            f"chosen={self.chosen} ({self.reason})"
        )


def choose_ample(
    model: Model,
    state: State,
    enabled: list[Action],
    on_stack: set[State],
    use_cycle_proviso: bool = True,
) -> tuple[list[Action], ReductionDecision]:
    enabled_names = [a.name for a in enabled]
    property_variables = model.property_variables

    candidate = None
    for action in enabled:
        if action.writes & property_variables:
            continue  # visible to the property: cannot be hidden
        if independent_of_all(action, model.actions):
            candidate = action
            break

    if candidate is None:
        decision = ReductionDecision(
            state, enabled_names, enabled_names,
            "no enabled action is both invisible and independent of all "
            "others; full expansion",
            None, False,
        )
        return list(enabled), decision

    successor = candidate.apply(state)
    if use_cycle_proviso and successor in on_stack:
        decision = ReductionDecision(
            state, enabled_names, enabled_names,
            f"cycle proviso: successor of {candidate.name!r} is on the DFS "
            f"stack; full expansion to protect ignored actions",
            candidate.name, True,
        )
        return list(enabled), decision

    decision = ReductionDecision(
        state, enabled_names, [candidate.name],
        f"{candidate.name!r} is enabled, invisible to the property and "
        f"independent of all other actions",
        candidate.name, False,
    )
    return [candidate], decision


def summarize_decisions(decisions: list[ReductionDecision]) -> dict[str, int]:
    total = len(decisions)
    singleton = sum(1 for d in decisions if len(d.chosen) == 1 and d.reduced)
    proviso = sum(1 for d in decisions if d.proviso_triggered)
    full = total - singleton - proviso
    return {
        "states_expanded": total,
        "singleton_reductions": singleton,
        "cycle_proviso_full_expansions": proviso,
        "other_full_expansions": full,
    }
