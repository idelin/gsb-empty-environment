"""Finite-state machines with tau (silent) transitions and nondeterminism.

Visible behavior is the set of visible traces: words over the visible
alphabet obtained by projecting runs onto non-tau labels.  Internal state
names are irrelevant to behavior.
"""

from __future__ import annotations

import hashlib
from dataclasses import dataclass

TAU = "τ"

_UNSET = object()


@dataclass(frozen=True)
class Transition:
    src: str
    label: str
    dst: str


class StateMachine:
    """A finite-state model.  ``initial=None`` denotes the empty machine
    (no initial state), which has no traces at all, not even the empty one."""

    def __init__(self, states, initial, transitions, version="v0"):
        self.states = frozenset(states)
        if initial is not None and initial not in self.states:
            raise ValueError(f"initial state {initial!r} not in state set")
        self.initial = initial
        normalized = []
        for t in transitions:
            t = t if isinstance(t, Transition) else Transition(*t)
            if t.src not in self.states or t.dst not in self.states:
                raise ValueError(f"transition endpoint outside state set: {t!r}")
            if not isinstance(t.label, str) or not t.label:
                raise ValueError(f"bad label: {t.label!r}")
            normalized.append(t)
        self.transitions = tuple(normalized)
        self.version = str(version)
        outgoing = {s: [] for s in self.states}
        for t in self.transitions:
            outgoing[t.src].append(t)
        self._outgoing = outgoing
        self.visible_alphabet = frozenset(
            t.label for t in self.transitions if t.label != TAU
        )

    def epsilon_closure(self, seeds):
        """States reachable via tau transitions only (handles tau loops)."""
        seen = set(seeds)
        stack = list(seeds)
        while stack:
            state = stack.pop()
            for t in self._outgoing[state]:
                if t.label == TAU and t.dst not in seen:
                    seen.add(t.dst)
                    stack.append(t.dst)
        return frozenset(seen)

    def start_subset(self):
        if self.initial is None:
            return frozenset()
        return self.epsilon_closure({self.initial})

    def subset_step(self, subset, label):
        """On-the-fly determinization step: eps-closure after ``label``."""
        if label == TAU:
            raise ValueError("only visible labels may be stepped")
        moved = set()
        for state in subset:
            for t in self._outgoing[state]:
                if t.label == label:
                    moved.add(t.dst)
        if not moved:
            return frozenset()
        return self.epsilon_closure(moved)

    def replay(self, word):
        """Subset of states reachable after a visible word; empty iff the
        word is not a visible trace of this machine."""
        subset = self.start_subset()
        for label in word:
            if not subset:
                break
            subset = self.subset_step(subset, label)
        return subset

    def accepts(self, word):
        return bool(self.replay(word))

    def fingerprint(self):
        """Content hash; changes whenever the structure changes."""
        canonical = (
            tuple(sorted(self.states)),
            self.initial,
            tuple(sorted((t.src, t.label, t.dst) for t in self.transitions)),
        )
        return hashlib.sha1(repr(canonical).encode("utf-8")).hexdigest()

    def updated(self, version, states=_UNSET, initial=_UNSET, transitions=_UNSET):
        """Return a new machine (a new version) with selected parts replaced."""
        return StateMachine(
            self.states if states is _UNSET else states,
            self.initial if initial is _UNSET else initial,
            self.transitions if transitions is _UNSET else transitions,
            version=version,
        )
