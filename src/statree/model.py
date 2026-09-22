"""Transition rules of a bottom-up ordered tree automaton.

A rule fires on a node when

* the node label equals :attr:`Rule.label`,
* the number of children (arity) equals :attr:`Rule.arity`,
* child *i* is in the state set :attr:`Rule.child_states[i]` (ordered), and
* the node's attribute mapping satisfies :attr:`Rule.condition`.

Firing assigns state :attr:`Rule.state` to the node.  Several rules may assign
several states to the same node; a nullary rule (arity 0) is a base case.
"""
from __future__ import annotations

from dataclasses import dataclass, field, replace
from typing import Any, Mapping

from .conditions import Always, Condition, ConditionError, parse_condition


class RuleError(ValueError):
    """Raised when a rule specification is malformed."""


@dataclass(frozen=True, slots=True)
class Signature:
    """Accepting states restricted to a node label and arity."""

    states: frozenset
    label: str | None = None
    arity: int | None = None

    def __post_init__(self) -> None:
        object.__setattr__(self, "states", frozenset(self.states))
        if not self.states:
            raise RuleError("signature must name at least one accepting state")
        if self.arity is not None and self.arity < 0:
            raise RuleError("arity must be non-negative")

    def matches(self, label: str, arity: int) -> bool:
        if self.label is not None and self.label != label:
            return False
        return self.arity is None or self.arity == arity

    @staticmethod
    def parse(spec: Any) -> "Signature":
        if isinstance(spec, Signature):
            return spec
        if isinstance(spec, str):
            return Signature({spec})
        if isinstance(spec, Mapping):
            states = spec["states"]
            return Signature(
                frozenset(states),
                spec.get("label"),
                spec.get("arity"),
            )
        if isinstance(spec, (tuple, list)):
            if len(spec) not in (1, 2, 3):
                raise RuleError("signature tuple must be (states[, label[, arity]])")
            seq = list(spec)
            states = seq[0]
            label = seq[1] if len(seq) > 1 else None
            arity = seq[2] if len(seq) > 2 else None
            return Signature(frozenset(states), label, arity)
        raise RuleError(f"cannot parse signature: {spec!r}")


@dataclass(frozen=True, slots=True)
class Rule:
    state: str
    label: str
    child_states: tuple[frozenset, ...]
    condition: Condition = field(default_factory=Always)
    name: str = ""

    @property
    def arity(self) -> int:
        return len(self.child_states)

    def __post_init__(self) -> None:
        if not self.state or not isinstance(self.state, str):
            raise RuleError("rule state must be a non-empty string")
        if not self.label or not isinstance(self.label, str):
            raise RuleError("rule label must be a non-empty string")
        if not isinstance(self.condition, Condition):
            raise RuleError("rule condition must be a Condition")
        norm = tuple(frozenset(s) for s in self.child_states)
        if any(not s for s in norm):
            raise RuleError("each child position must accept at least one state")
        object.__setattr__(self, "child_states", norm)
        if not self.name:
            object.__setattr__(self, "name", self._default_name())

    def _default_name(self) -> str:
        kids = ",".join("|".join(sorted(s)) for s in self.child_states)
        return f"{self.state}:{self.label}({kids})"

    def applicable(self, label: str, arity: int, attr_fn) -> bool:
        """Check label/arity/attributes; child-state matching is positional."""
        return (
            label == self.label
            and arity == self.arity
            and self.condition.holds(attr_fn())
        )

    def with_name(self, name: str) -> "Rule":
        return replace(self, name=name)

    @staticmethod
    def parse(spec: Any) -> "Rule":
        if isinstance(spec, Rule):
            return spec
        if isinstance(spec, Mapping):
            try:
                state = spec["state"]
                label = spec["label"]
                child_states = spec.get("child_states", ())
            except KeyError as exc:
                raise RuleError(f"rule dict needs {exc.args[0]!r}") from exc
            if not isinstance(child_states, (tuple, list)):
                raise RuleError("child_states must be a list/tuple of sets")
            try:
                cond = parse_condition(spec.get("condition"))
            except ConditionError as exc:
                raise RuleError(str(exc)) from exc
            return Rule(
                state=state,
                label=label,
                child_states=tuple(frozenset(s) for s in child_states),
                condition=cond,
                name=spec.get("name", ""),
            )
        raise RuleError(f"cannot parse rule: {spec!r}")
