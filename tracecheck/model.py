"""Finite-state machine model with silent (tau) transitions.

The verified objects are finite models only: states, alphabet and the
transition set must all be finite and are validated at construction time.
"""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass, replace

TAU = None  # label used for silent (internal) transitions
_TAU_TOKEN = "\x00tau"


@dataclass(frozen=True)
class StateMachine:
    """A finite nondeterministic machine with visible actions and tau steps.

    Its visible behaviour is the set of finite visible words (traces) that
    label some path from the initial state, ignoring tau steps.  State names
    are internal and irrelevant to the behaviour.
    """

    states: frozenset
    alphabet: frozenset
    transitions: frozenset  # items are (src, label_or_TAU, dst)
    initial: object
    version: str = "v1"

    def __post_init__(self):
        if self.initial not in self.states:
            raise ValueError(f"initial state {self.initial!r} not in states")
        for src, label, dst in self.transitions:
            if src not in self.states or dst not in self.states:
                raise ValueError(
                    f"transition ({src!r}, {label!r}, {dst!r}) references unknown state"
                )
            if label is not TAU and label not in self.alphabet:
                raise ValueError(f"visible label {label!r} not in alphabet")

    def fingerprint(self) -> str:
        """Content hash of the structure; any model edit changes it."""
        payload = {
            "states": sorted(map(repr, self.states)),
            "alphabet": sorted(map(repr, self.alphabet)),
            "transitions": sorted(
                (
                    repr(src),
                    _TAU_TOKEN if label is TAU else repr(label),
                    repr(dst),
                )
                for src, label, dst in self.transitions
            ),
            "initial": repr(self.initial),
        }
        blob = json.dumps(payload, sort_keys=True).encode("utf-8")
        return hashlib.sha256(blob).hexdigest()

    def with_version(self, version: str) -> "StateMachine":
        return replace(self, version=version)


def make_machine(states, alphabet, transitions, initial, version="v1") -> StateMachine:
    """Convenience builder accepting plain iterables."""
    return StateMachine(
        states=frozenset(states),
        alphabet=frozenset(alphabet),
        transitions=frozenset(transitions),
        initial=initial,
        version=version,
    )
