"""Finite-state model definitions for the liveness checker.

A model is a directed graph of states connected by action-labelled
transitions. States may carry atomic propositions; the checker cares
about two of them: "req" (a request is outstanding) and "resp" (the
request has been answered).
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Transition:
    src: str
    action: str
    dst: str


class Model:
    def __init__(self, name: str, initial: str):
        self.name = name
        self.initial = initial
        self.labels: dict[str, frozenset[str]] = {initial: frozenset()}
        self.transitions: list[Transition] = []
        self._outgoing: dict[str, list[Transition]] = {}

    def add_state(self, state: str, labels=()) -> None:
        self.labels[state] = frozenset(labels)

    def add_transition(self, src: str, action: str, dst: str) -> None:
        for state in (src, dst):
            self.labels.setdefault(state, frozenset())
        transition = Transition(src, action, dst)
        self.transitions.append(transition)
        self._outgoing.setdefault(src, []).append(transition)

    def states(self) -> list[str]:
        return list(self.labels)

    def outgoing(self, state: str) -> list[Transition]:
        return self._outgoing.get(state, [])

    def actions_enabled(self, state: str) -> frozenset[str]:
        return frozenset(t.action for t in self.outgoing(state))
