"""Depth-first exploration with ample-set partial order reduction.

Reduction theory implemented here (see Clarke/Grumberg/Peled, "Model
Checking", ch. 10):

* Independence is derived from read/write sets: two actions are dependent
  iff one writes a variable the other reads or writes.  In particular two
  actions writing the same variable are NEVER independent.
* C0: the ample set is empty iff the enabled set is empty.
* C1 (persistence): no action dependent on an ample-set action can execute
  before an ample-set action does.  Computed conservatively: a seed's
  closure adds every enabled dependent action and is rejected if a dependent
  action is currently disabled (it might become enabled via other actions).
* C2 (cycle proviso): every cycle of the reduced graph must contain a fully
  expanded state.  Enforced on-the-fly: if a not-fully-expanded state has a
  successor on the DFS stack, it is fully expanded.  This protects actions
  that would otherwise be ignored forever along a loop.
* C3 (visibility): if the ample set is a proper subset of the enabled set,
  none of its actions may write a variable read by the safety property.

Together these preserve all deadlocks and the truth of the (stutter
invariant) safety property.  If the search is truncated by resource limits
the verdict stays PENDING instead of SAFE.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import List, Optional

from .model import Action, Model, State, format_state

SAFE = "safe"
UNSAFE = "unsafe"
PENDING = "pending"


def dependent(a: Action, b: Action) -> bool:
    """Read/write-set dependency; write-write on one var is dependent."""
    return bool(a.writes & (b.reads | b.writes)) or bool(
        b.writes & (a.reads | a.writes)
    )


def enabled_actions(model: Model, state: State) -> List[Action]:
    return [a for a in model.actions if a.guard(state)]


def _visible(action: Action, model: Model) -> bool:
    return bool(model.property_fn) and bool(action.writes & model.property_vars)


def choose_ample(model: Model, state: State, enabled: List[Action]):
    """Pick an ample subset of ``enabled``; returns (ample, fully, notes)."""
    notes: List[str] = []
    if not enabled:
        return [], True, notes
    enabled_names = {a.name for a in enabled}
    for seed in enabled:
        selected = {seed.name}
        rejected = None
        while True:
            grew = False
            for c_name in sorted(selected):
                c = model.by_name[c_name]
                for d in model.actions:
                    if d.name in selected or not dependent(c, d):
                        continue
                    if d.name in enabled_names:
                        selected.add(d.name)
                        grew = True
                    else:
                        rejected = (
                            f"seed '{seed.name}': dependent action '{d.name}' is "
                            f"disabled here and could fire before the candidate set; "
                            f"seed rejected"
                        )
                        break
                if rejected:
                    break
            if rejected or not grew:
                break
        if rejected:
            notes.append(rejected)
            continue
        if selected >= enabled_names:
            notes.append(
                f"seed '{seed.name}': dependency closure covers every enabled "
                f"action; full expansion"
            )
            return list(enabled), True, notes
        visible = sorted(n for n in selected if _visible(model.by_name[n], model))
        if visible:
            notes.append(
                f"seed '{seed.name}': candidate {sorted(selected)} writes property "
                f"variables {sorted(model.property_vars)} via {visible}; seed rejected"
            )
            continue
        ignored = sorted(enabled_names - selected)
        notes.append(
            f"seed '{seed.name}': persistent set {sorted(selected)} selected; "
            f"ignored {ignored} are independent of every selected action"
        )
        ample = [a for a in enabled if a.name in selected]
        return ample, False, notes
    notes.append("no persistent proper subset found; full expansion")
    return list(enabled), True, notes


@dataclass
class StepRecord:
    """Reduction evidence for one expanded state."""

    state: State
    enabled: List[str]
    chosen: List[str]
    fully_expanded: bool
    notes: List[str] = field(default_factory=list)


@dataclass
class Result:
    verdict: str  # SAFE | UNSAFE | PENDING
    kind: Optional[str]  # 'property_violation' | 'deadlock' | None
    trace: List[str]  # replayable action-name sequence (counterexample)
    states_explored: int
    transitions: int
    truncated: bool
    log: List[StepRecord] = field(default_factory=list)


class ReplayError(Exception):
    pass


def replay(model: Model, trace: List[str]) -> State:
    """Re-execute a trace from the initial state; returns the final state."""
    state = model.initial
    for name in trace:
        action = model.by_name.get(name)
        if action is None:
            raise ReplayError(f"unknown action '{name}'")
        if not action.guard(state):
            raise ReplayError(
                f"action '{name}' not enabled at {format_state(state)}"
            )
        state = action.effect(state)
    return state


def explore(
    model: Model,
    use_por: bool = True,
    cycle_proviso: bool = True,
    max_states: Optional[int] = None,
    max_depth: Optional[int] = None,
) -> Result:
    """DFS over the (optionally reduced) state graph.

    Returns UNSAFE with a replayable trace on a property violation or a
    deadlock, SAFE when the graph was fully explored, and PENDING when the
    exploration was truncated by ``max_states``/``max_depth``.
    """
    log: List[StepRecord] = []
    visited = {model.initial}
    transitions = 0
    truncated = False
    action_path: List[str] = []  # len == len(stack) - 1
    # frames: [state, actions_to_explore or None, next_index]
    stack = [[model.initial, None, 0]]
    on_stack = {model.initial}

    def holds(state: State) -> bool:
        return model.property_fn is None or model.property_fn(state)

    if not holds(model.initial):
        return Result(UNSAFE, "property_violation", [], 1, 0, False, log)

    while stack:
        frame = stack[-1]
        state = frame[0]
        if frame[1] is None:
            enabled = enabled_actions(model, state)
            if not enabled:
                if model.check_deadlock:
                    return Result(
                        UNSAFE, "deadlock", list(action_path),
                        len(visited), transitions, truncated, log,
                    )
                frame[1] = []
                log.append(StepRecord(state, [], [], True,
                                      ["no enabled actions (deadlock check off)"]))
                continue
            if use_por:
                ample, fully, notes = choose_ample(model, state, enabled)
                if cycle_proviso and not fully:
                    closing = [a.name for a in ample if a.effect(state) in on_stack]
                    if closing:
                        notes.append(
                            f"cycle proviso: successor via {closing} is on the DFS "
                            f"stack; expanding all enabled actions so ignored "
                            f"actions cannot be postponed forever"
                        )
                        ample, fully = list(enabled), True
            else:
                ample, fully, notes = list(enabled), True, [
                    "full enumeration (POR disabled)"
                ]
            log.append(
                StepRecord(
                    state,
                    [a.name for a in enabled],
                    [a.name for a in ample],
                    fully,
                    notes,
                )
            )
            frame[1] = ample
        if frame[2] < len(frame[1]):
            action = frame[1][frame[2]]
            frame[2] += 1
            nxt = action.effect(state)
            transitions += 1
            if not holds(nxt):
                return Result(
                    UNSAFE, "property_violation", action_path + [action.name],
                    len(visited), transitions, truncated, log,
                )
            if nxt not in visited:
                if max_states is not None and len(visited) >= max_states:
                    truncated = True
                    continue
                if max_depth is not None and len(stack) >= max_depth:
                    truncated = True
                    continue
                visited.add(nxt)
                on_stack.add(nxt)
                action_path.append(action.name)
                stack.append([nxt, None, 0])
        else:
            stack.pop()
            on_stack.discard(state)
            if action_path:
                action_path.pop()

    verdict = PENDING if truncated else SAFE
    return Result(verdict, None, [], len(visited), transitions, truncated, log)


def format_evidence(result: Result, max_steps: Optional[int] = None) -> str:
    """Render the reduction-selection evidence collected during search."""
    lines = []
    for i, rec in enumerate(result.log):
        if max_steps is not None and i >= max_steps:
            lines.append(f"... ({len(result.log) - i} more states)")
            break
        lines.append(f"#{i} state={format_state(rec.state)}")
        lines.append(
            f"   enabled={rec.enabled} chosen={rec.chosen} "
            f"fully_expanded={rec.fully_expanded}"
        )
        for note in rec.notes:
            lines.append(f"   - {note}")
    return "\n".join(lines)
