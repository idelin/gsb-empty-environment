"""A declarative finite-state model.

The model is fully defined by finite, inspectable data (a transition table
plus an initial-state set and a bad-state predicate), so that an independent
checker can re-derive every claim in a certificate.  States are JSON values
(typically ``int`` or ``str``); the canonical fingerprint pins the exact
model the certificate was produced against.
"""

from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass, field
from typing import Collection, Mapping, Sequence


class ModelValidationError(ValueError):
    """Raised when a model definition is not a valid finite transition system."""


def canonical_model_data(
    states: Collection[object],
    initial: Collection[object],
    transitions: Mapping[object, Sequence[object]],
    bad_states: Collection[object],
) -> dict[str, object]:
    """Return a normalized, JSON-serializable description of a model."""
    try:
        state_sorted = sorted(states, key=lambda s: json.dumps(s, sort_keys=True))
    except TypeError as exc:  # pragma: no cover - defensive
        raise ModelValidationError(f"states must be JSON-serializable: {exc}") from exc
    try:
        initial_set = {json.dumps(s) for s in initial}
    except TypeError as exc:
        raise ModelValidationError(f"initial states must be JSON-serializable: {exc}") from exc
    table: dict[str, list[object]] = {}
    for source in transitions:
        table[json.dumps(source, sort_keys=True)] = list(transitions[source])
    try:
        bad_set = {json.dumps(s) for s in bad_states}
    except TypeError as exc:
        raise ModelValidationError(f"bad states must be JSON-serializable: {exc}") from exc
    return {
        "states": state_sorted,
        "initial": [s for s in state_sorted if json.dumps(s, sort_keys=True) in initial_set],
        "transitions": {
            json.dumps(s, sort_keys=True): sorted(
                table.get(json.dumps(s, sort_keys=True), []),
                key=lambda t: json.dumps(t, sort_keys=True),
            )
            for s in state_sorted
        },
        "bad_states": [s for s in state_sorted if json.dumps(s, sort_keys=True) in bad_set],
    }


def fingerprint_of(data: Mapping[str, object]) -> str:
    """SHA-256 fingerprint of the canonical model description."""
    blob = json.dumps(data, sort_keys=True, separators=(",", ":")).encode("utf-8")
    return hashlib.sha256(blob).hexdigest()


@dataclass(frozen=True)
class FiniteModel:
    """A finite Kripke structure restricted to a declared state set.

    ``transitions`` maps each source state to its full successor list; sources
    without an outgoing edge act as terminal states.  The property to verify is
    an invariant: *no reachable state may belong to* ``bad_states``.
    """

    name: str
    states: frozenset[object]
    initial: frozenset[object]
    transitions: Mapping[object, tuple[object, ...]]
    bad_states: frozenset[object]
    fingerprint: str = field(compare=False)

    @classmethod
    def build(
        cls,
        name: str,
        states: Collection[object],
        initial: Collection[object],
        transitions: Mapping[object, Collection[object]],
        bad_states: Collection[object] = (),
    ) -> "FiniteModel":
        state_keys: dict[str, object] = {}
        for state in states:
            key = json.dumps(state, sort_keys=True)
            if key in state_keys:
                raise ModelValidationError(
                    f"duplicate state encoding {key!r} in declared states"
                )
            state_keys[key] = state

        def require_known(value: object, where: str) -> None:
            if json.dumps(value, sort_keys=True) not in state_keys:
                raise ModelValidationError(f"{where} references unknown state {value!r}")

        for state in initial:
            require_known(state, "initial set")
        for source in transitions:
            require_known(source, "transition source")
            for target in transitions[source]:
                require_known(target, f"transition from {source!r}")
        for state in bad_states:
            require_known(state, "bad-state set")

        if not initial:
            raise ModelValidationError("model has no initial states")

        table = {
            source: tuple(transitions[source])
            for source in states
            if source in transitions
        }
        data = canonical_model_data(states, initial, table, bad_states)
        return cls(
            name=name,
            states=frozenset(states),
            initial=frozenset(initial),
            transitions=table,
            bad_states=frozenset(bad_states),
            fingerprint=fingerprint_of(data),
        )

    def successors(self, state: object) -> tuple[object, ...]:
        if state not in self.states:
            raise KeyError(f"state not in model: {state!r}")
        return self.transitions.get(state, ())

    def is_bad(self, state: object) -> bool:
        return state in self.bad_states

    def canonical_data(self) -> dict[str, object]:
        return canonical_model_data(
            self.states, self.initial, self.transitions, self.bad_states
        )

    def to_certificate_model(self) -> dict[str, object]:
        return {
            "name": self.name,
            "fingerprint": self.fingerprint,
        }
