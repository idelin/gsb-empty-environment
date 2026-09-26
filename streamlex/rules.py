"""Lexical rules and lexical-state actions."""
from __future__ import annotations

from dataclasses import dataclass

from .dfa import DFA

_ACTION_KINDS = ("none", "push", "pop", "switch")


@dataclass(frozen=True)
class Action:
    """What happens to the lexical state stack after a token is emitted.

    ``kind`` is one of ``"none"``, ``"push"``, ``"pop"``, ``"switch"``;
    ``state`` carries the target state for ``push``/``switch``.
    """

    kind: str
    state: str | None = None

    def __post_init__(self):
        if self.kind not in _ACTION_KINDS:
            raise ValueError(f"unknown action kind {self.kind!r}")
        if self.kind in ("push", "switch") and self.state is None:
            raise ValueError(f"action {self.kind!r} requires a target state")
        if self.kind in ("none", "pop") and self.state is not None:
            raise ValueError(f"action {self.kind!r} takes no target state")


NO_ACTION = Action("none")


def Push(state: str) -> Action:
    """Push ``state`` onto the lexical state stack."""
    return Action("push", state)


def Pop() -> Action:
    """Pop the top lexical state, returning to the previous one."""
    return Action("pop")


def Switch(state: str) -> Action:
    """Replace the top lexical state with ``state``."""
    return Action("switch", state)


class Rule:
    """One lexical rule: an explicit DFA plus priority, states and action.

    ``priority`` is an integer; lower wins when two rules accept the same
    longest lexeme.  ``states`` is the set of lexical states in which the
    rule is active.  ``emit=False`` makes the rule silent (e.g. whitespace).
    """

    def __init__(self, name, dfa, *, priority, states, action=NO_ACTION, emit=True):
        if not name:
            raise ValueError("rule name must be non-empty")
        if not isinstance(dfa, DFA):
            raise TypeError("dfa must be a streamlex.dfa.DFA")
        if not isinstance(priority, int):
            raise TypeError("priority must be an int (lower wins ties)")
        states = frozenset(states)
        if not states:
            raise ValueError("rule must be active in at least one lexical state")
        if not isinstance(action, Action):
            raise TypeError("action must be a streamlex.rules.Action")
        self.name = name
        self.dfa = dfa
        self.priority = priority
        self.states = states
        self.action = action
        self.emit = bool(emit)

    def __repr__(self):
        return f"Rule({self.name!r}, priority={self.priority}, states={sorted(self.states)!r})"
