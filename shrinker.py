"""Property-preserving shrinker for finite-state model counterexamples.

Given a *fixed* finite-state model and a counterexample triple
(initial configuration, action trace, fault set) that violates a property,
greedily shrink each component.  Every candidate is re-replayed independently
from scratch and is accepted only if replay triggers the *same* violation
(identical error signature).  Candidates whose action preconditions no longer
hold are rejected, and a different error signature never counts as the same
counterexample.

Termination semantics:

* ``local_minimum`` -- every single-deletion candidate (config entry, trace
  action, fault) was replayed and rejected; the result is 1-minimal.
* ``undecided`` -- the replay budget was exhausted before the search space
  was fully explored; no minimality claim is made (in particular, nothing
  about global minimality is ever claimed).
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Hashable, Mapping, Protocol

ErrorSignature = Hashable


class Model(Protocol):
    """A finite-state model under a fixed transition semantics."""

    def initial_state(self, config: Mapping[str, Any]) -> Any:
        """Build the initial state; missing config keys fall back to defaults."""

    def precondition(self, state: Any, action: Any) -> bool:
        """Whether ``action`` is enabled in ``state``."""

    def step(self, state: Any, action: Any, faults: frozenset) -> Any:
        """Deterministic transition under the active fault set."""

    def check(
        self, state: Any, action: Any | None, faults: frozenset
    ) -> ErrorSignature | None:
        """Return an error signature if ``state`` violates the property."""


@dataclass(frozen=True)
class ReplayResult:
    outcome: str  # "violation" | "precondition_failed" | "clean"
    signature: ErrorSignature | None = None
    step: int | None = None


def replay(
    model: Model,
    config: Mapping[str, Any],
    trace: list,
    faults: frozenset,
) -> ReplayResult:
    """Independently replay a counterexample candidate from scratch."""
    state = model.initial_state(config)
    signature = model.check(state, None, faults)
    if signature is not None:
        return ReplayResult("violation", signature, None)
    for index, action in enumerate(trace):
        if not model.precondition(state, action):
            return ReplayResult("precondition_failed", None, index)
        state = model.step(state, action, faults)
        signature = model.check(state, action, faults)
        if signature is not None:
            return ReplayResult("violation", signature, index)
    return ReplayResult("clean", None, None)


@dataclass
class ShrinkResult:
    config: dict
    trace: list
    faults: frozenset
    signature: ErrorSignature
    replays_used: int
    status: str  # "local_minimum" | "undecided"

    @property
    def is_local_minimum(self) -> bool:
        return self.status == "local_minimum"


def shrink(
    model: Model,
    config: Mapping[str, Any],
    trace: list,
    faults: frozenset,
    *,
    signature: ErrorSignature | None = None,
    max_replays: int = 1000,
) -> ShrinkResult:
    """Shrink a counterexample while preserving its exact violation.

    ``max_replays`` bounds the total number of replays (including the initial
    one).  If the budget is exhausted, the returned status is ``undecided``;
    the returned triple is still a valid, replayable counterexample with the
    same signature, just not a proven local minimum.
    """
    if max_replays < 1:
        raise ValueError("max_replays must be >= 1")
    initial = replay(model, config, trace, faults)
    replays_used = 1
    if initial.outcome != "violation":
        raise ValueError(
            f"initial input does not violate the property (outcome: {initial.outcome})"
        )
    target = initial.signature if signature is None else signature

    config = dict(config)
    trace = list(trace)
    faults = frozenset(faults)
    budget_exhausted = False

    def probe(candidate_config, candidate_trace, candidate_faults) -> bool:
        nonlocal replays_used, budget_exhausted
        if budget_exhausted or replays_used >= max_replays:
            budget_exhausted = True
            return False
        replays_used += 1
        outcome = replay(model, candidate_config, candidate_trace, candidate_faults)
        return outcome.outcome == "violation" and outcome.signature == target

    improved = True
    while improved and not budget_exhausted:
        improved = False

        for key in sorted(config, key=repr):
            if budget_exhausted:
                break
            candidate = {k: v for k, v in config.items() if k != key}
            if probe(candidate, trace, faults):
                config = candidate
                improved = True

        index = 0
        while index < len(trace) and not budget_exhausted:
            candidate = trace[:index] + trace[index + 1 :]
            if probe(config, candidate, faults):
                trace = candidate
                improved = True
            else:
                index += 1

        for fault in sorted(faults, key=repr):
            if budget_exhausted:
                break
            candidate = faults - {fault}
            if probe(config, trace, candidate):
                faults = candidate
                improved = True

    status = "undecided" if budget_exhausted else "local_minimum"
    return ShrinkResult(config, trace, faults, target, replays_used, status)
