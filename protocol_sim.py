from __future__ import annotations

from collections.abc import Iterator
from dataclasses import dataclass, field
from enum import Enum
from typing import NamedTuple


class Direction(Enum):
    INPUT = "input"
    OUTPUT = "output"


@dataclass(frozen=True)
class Action:
    name: str
    direction: Direction

    @property
    def label(self) -> str:
        marker = "?" if self.direction is Direction.INPUT else "!"
        return f"{self.name}{marker}"


@dataclass(frozen=True)
class Transition:
    source: str
    action: Action
    target: str

    @property
    def label(self) -> str:
        return f"{self.source} --{self.action.label}--> {self.target}"


@dataclass(frozen=True)
class Alphabet:
    actions: frozenset[Action]

    def __init__(self, actions: frozenset[Action] | set[Action] | tuple[Action, ...]):
        object.__setattr__(self, "actions", frozenset(actions))

    def has(self, action: Action) -> bool:
        return action in self.actions


@dataclass(frozen=True)
class StateMachine:
    name: str
    states: frozenset[str]
    initial: str
    alphabet: Alphabet
    transitions: frozenset[Transition]
    outgoing: dict[str, dict[Action, frozenset[str]]] = field(
        init=False, repr=False, compare=False
    )

    def __init__(
        self,
        name: str,
        states: set[str] | tuple[str, ...] | frozenset[str],
        initial: str,
        alphabet: Alphabet,
        transitions: set[Transition] | tuple[Transition, ...] | frozenset[Transition],
    ) -> None:
        state_set = frozenset(states)
        transition_set = frozenset(transitions)

        if not state_set:
            raise ValueError("state machine must contain at least one state")
        if initial not in state_set:
            raise ValueError(f"initial state {initial!r} is not a state")
        for transition in transition_set:
            if transition.source not in state_set or transition.target not in state_set:
                raise ValueError(f"transition leaves declared states: {transition.label}")
            if not alphabet.has(transition.action):
                raise ValueError(f"action {transition.action.label} is not in alphabet")

        outgoing: dict[str, dict[Action, set[str]]] = {state: {} for state in state_set}
        for transition in transition_set:
            outgoing[transition.source].setdefault(transition.action, set()).add(
                transition.target
            )
        frozen_outgoing = {
            state: {action: frozenset(targets) for action, targets in actions.items()}
            for state, actions in outgoing.items()
        }

        object.__setattr__(self, "name", name)
        object.__setattr__(self, "states", state_set)
        object.__setattr__(self, "initial", initial)
        object.__setattr__(self, "alphabet", alphabet)
        object.__setattr__(self, "transitions", transition_set)
        object.__setattr__(self, "outgoing", frozen_outgoing)

    def targets(self, state: str, action: Action) -> frozenset[str]:
        return self.outgoing.get(state, {}).get(action, frozenset())


@dataclass(frozen=True)
class Step:
    kind: str
    state_pair: tuple[str, str]
    spec_transition: Transition | None
    candidate_transition: Transition | None

    def render(self) -> str:
        spec_text = self.spec_transition.label if self.spec_transition else "<none>"
        candidate_text = (
            self.candidate_transition.label
            if self.candidate_transition
            else "<none>"
        )
        return f"{self.kind}: spec {spec_text}; candidate {candidate_text}"


@dataclass(frozen=True)
class FailurePath:
    relation: str
    initial_pair: tuple[str, str]
    steps: tuple[Step, ...]
    terminal_kind: str
    terminal_state_pair: tuple[str, str]
    action: Action
    input_trace: tuple[Action, ...]
    output_trace: tuple[Action, ...]

    def render(self) -> str:
        lines = [
            f"{self.relation} failed from {self._pair(self.initial_pair)}",
            *[self._render_step(step) for step in self.steps],
            f"terminal: {self.terminal_kind} {self.action.label} at "
            f"{self._pair(self.terminal_state_pair)}",
            f"input trace: {self._trace(self.input_trace)}",
            f"output trace: {self._trace(self.output_trace)}",
        ]
        return "\n".join(lines)

    @staticmethod
    def _pair(pair: tuple[str, str]) -> str:
        spec_state, candidate_state = pair
        return f"(spec={spec_state}, candidate={candidate_state})"

    def _render_step(self, step: Step) -> str:
        return f"{self._pair(step.state_pair)} {step.render()}"

    @staticmethod
    def _trace(trace: tuple[Action, ...]) -> str:
        return ", ".join(action.label for action in trace) if trace else "<empty>"


@dataclass(frozen=True)
class SimulationResult:
    satisfied: bool
    relation: str
    iterations: int
    failure: FailurePath | None = None


class _Witness(NamedTuple):
    kind: str
    spec_transition: Transition | None
    candidate_transition: Transition | None
    next_pair: tuple[str, str] | None
    terminal_kind: str
    action: Action


def check_alternating_simulation(
    spec: StateMachine, candidate: StateMachine
) -> SimulationResult:
    """Check candidate <= spec for an input-enabled-style IO branch convention.

    Inputs are contravariant at the action-set level: an implementation may
    accept an extra input, but it must accept every input enabled by the spec.
    Outputs are covariant: every candidate output must be allowed by the spec,
    while a candidate may omit an optional spec output.
    """

    initial_pair = (spec.initial, candidate.initial)
    relation = {
        (spec_state, candidate_state)
        for spec_state in spec.states
        for candidate_state in candidate.states
    }
    witnesses: dict[tuple[str, str], _Witness] = {}
    iterations = 0

    while True:
        iterations += 1
        snapshot = frozenset(relation)
        removed: dict[tuple[str, str], _Witness] = {}
        for pair in sorted(snapshot):
            witness = _first_violation(spec, candidate, pair, snapshot)
            if witness is not None:
                removed[pair] = witness
        if not removed:
            break
        relation.difference_update(removed)
        witnesses.update(removed)

    if initial_pair in relation:
        return SimulationResult(True, "alternating simulation", iterations)

    failure = _build_failure_path(initial_pair, witnesses)
    return SimulationResult(False, "alternating simulation", iterations, failure)


def _first_violation(
    spec: StateMachine,
    candidate: StateMachine,
    pair: tuple[str, str],
    relation: frozenset[tuple[str, str]],
) -> _Witness | None:
    spec_state, candidate_state = pair

    for action in sorted(spec.alphabet.actions, key=_action_key):
        spec_targets = spec.targets(spec_state, action)
        candidate_targets = candidate.targets(candidate_state, action)
        if action.direction is Direction.INPUT and spec_targets:
            if not candidate_targets:
                spec_target = sorted(spec_targets)[0]
                return _Witness(
                    "required input",
                    Transition(spec_state, action, spec_target),
                    None,
                    None,
                    "missing required input",
                    action,
                )
            for required_target in sorted(spec_targets):
                related_targets = [
                    implemented_target
                    for implemented_target in sorted(candidate_targets)
                    if (required_target, implemented_target) in relation
                ]
                if not related_targets:
                    target = sorted(candidate_targets)[0]
                    return _Witness(
                        "required input branch",
                        Transition(spec_state, action, required_target),
                        Transition(candidate_state, action, target),
                        (required_target, target),
                        "removed successor",
                        action,
                    )

    for action in sorted(candidate.alphabet.actions, key=_action_key):
        spec_targets = spec.targets(spec_state, action)
        candidate_targets = candidate.targets(candidate_state, action)
        if action.direction is Direction.OUTPUT and candidate_targets:
            if not spec_targets:
                target = sorted(candidate_targets)[0]
                return _Witness(
                    "candidate output",
                    None,
                    Transition(candidate_state, action, target),
                    None,
                    "extra output",
                    action,
                )
            for candidate_target in sorted(candidate_targets):
                related_targets = [
                    spec_target
                    for spec_target in sorted(spec_targets)
                    if (spec_target, candidate_target) in relation
                ]
                if not related_targets:
                    target = sorted(spec_targets)[0]
                    return _Witness(
                        "candidate output branch",
                        Transition(spec_state, action, target),
                        Transition(candidate_state, action, candidate_target),
                        (target, candidate_target),
                        "removed successor",
                        action,
                    )

    return None


def _action_key(action: Action) -> tuple[str, str]:
    return action.name, action.direction.value


def _build_failure_path(
    initial_pair: tuple[str, str],
    witnesses: dict[tuple[str, str], _Witness],
) -> FailurePath:
    steps: list[Step] = []
    inputs: list[Action] = []
    outputs: list[Action] = []
    current = initial_pair
    visited = set()

    while current in witnesses and current not in visited:
        visited.add(current)
        witness = witnesses[current]
        steps.append(
            Step(
                witness.kind,
                current,
                witness.spec_transition,
                witness.candidate_transition,
            )
        )
        if witness.action.direction is Direction.INPUT:
            inputs.append(witness.action)
        else:
            outputs.append(witness.action)
        if witness.next_pair is None:
            return FailurePath(
                "alternating simulation",
                initial_pair,
                tuple(steps),
                witness.terminal_kind,
                current,
                witness.action,
                tuple(inputs),
                tuple(outputs),
            )
        current = witness.next_pair

    raise RuntimeError("internal error: witness chain did not reach a terminal cause")


@dataclass(frozen=True)
class ReplayedFrame:
    index: int
    kind: str
    state_pair: tuple[str, str]
    spec_transition: Transition | None
    candidate_transition: Transition | None

    def render(self) -> str:
        spec_text = (
            self.spec_transition.label if self.spec_transition else "<none>"
        )
        candidate_text = (
            self.candidate_transition.label
            if self.candidate_transition
            else "<none>"
        )
        spec_state, candidate_state = self.state_pair
        return (
            f"#{self.index} {self.kind} "
            f"(spec={spec_state}, candidate={candidate_state}) "
            f"spec {spec_text}; candidate {candidate_text}"
        )


class ReplayError(ValueError):
    pass


def replay_failure(
    spec: StateMachine,
    candidate: StateMachine,
    failure: FailurePath,
) -> tuple[ReplayedFrame, ...]:
    """Independently execute a reported failure and return all frames."""

    return tuple(iter_replay_failure(spec, candidate, failure))


def iter_replay_failure(
    spec: StateMachine,
    candidate: StateMachine,
    failure: FailurePath,
) -> Iterator[ReplayedFrame]:
    """Yield each replay frame, then verify the terminal obstruction."""

    current_spec, current_candidate = failure.initial_pair

    for index, step in enumerate(failure.steps, start=1):
        if step.state_pair != (current_spec, current_candidate):
            raise ReplayError(f"frame {index} does not continue from current pair")

        spec_transition = step.spec_transition
        candidate_transition = step.candidate_transition

        if spec_transition is not None:
            if spec_transition.source != current_spec:
                raise ReplayError(f"frame {index} spec transition source is wrong")
            if spec_transition.target not in spec.targets(
                current_spec, spec_transition.action
            ):
                raise ReplayError(f"frame {index} spec transition is not enabled")

        if candidate_transition is not None:
            if candidate_transition.source != current_candidate:
                raise ReplayError(f"frame {index} candidate transition source is wrong")
            if candidate_transition.target not in candidate.targets(
                current_candidate, candidate_transition.action
            ):
                raise ReplayError(
                    f"frame {index} candidate transition is not enabled"
                )

        yield ReplayedFrame(
            index,
            step.kind,
            step.state_pair,
            spec_transition,
            candidate_transition,
        )

        if step.kind == "required input":
            if candidate_transition is not None:
                raise ReplayError("missing-input frame must not have candidate branch")
            if spec_transition is None:
                raise ReplayError("input frame must have a spec branch")
        elif step.kind == "candidate output":
            if spec_transition is not None:
                raise ReplayError("extra-output frame must not have spec branch")
            if candidate_transition is None:
                raise ReplayError("output frame must have candidate branch")
        elif step.kind in {"required input branch", "candidate output branch"}:
            if spec_transition is None or candidate_transition is None:
                raise ReplayError(f"frame {index} needs both compared branches")
            if spec_transition.action != candidate_transition.action:
                raise ReplayError(f"frame {index} compares different actions")
            current_spec, current_candidate = (
                spec_transition.target,
                candidate_transition.target,
            )
        else:
            raise ReplayError(f"frame {index} has unsupported kind {step.kind!r}")

    terminal = failure.terminal_state_pair
    if terminal != (current_spec, current_candidate):
        raise ReplayError("path does not end at its reported terminal pair")

    action = failure.action
    spec_state, candidate_state = terminal
    if failure.terminal_kind == "missing required input":
        if not spec.targets(spec_state, action):
            raise ReplayError("terminal spec input is not enabled")
        if candidate.targets(candidate_state, action):
            raise ReplayError("candidate unexpectedly enables terminal input")
    elif failure.terminal_kind == "extra output":
        if spec.targets(spec_state, action):
            raise ReplayError("spec unexpectedly allows terminal output")
        if not candidate.targets(candidate_state, action):
            raise ReplayError("candidate does not enable terminal output")
    else:
        raise ReplayError(f"unsupported terminal kind {failure.terminal_kind!r}")
