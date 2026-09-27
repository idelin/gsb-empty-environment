"""Finite labelled transition systems (LTS): the only kind of verification object.

Only explicitly given *finite* state models are accepted.  States are strings
and an action label equal to ``TAU`` ("tau") denotes an internal (silent)
action.  A state without outgoing transitions is a deadlock state; deadlocks
are not hidden by the simulation checks.
"""

from __future__ import annotations

import json
from collections.abc import Iterable
from dataclasses import dataclass, field
from pathlib import Path

TAU = "tau"


@dataclass
class LTS:
    """A finite labelled transition system.

    ``transitions`` maps a source state to an ordered list of
    ``(label, target)`` pairs.  Transition order is preserved because file
    order makes all outputs deterministic, but duplicate edges are removed.
    """

    name: str
    initial: str
    transitions: dict[str, list[tuple[str, str]]] = field(default_factory=dict)

    def __post_init__(self) -> None:
        if not isinstance(self.name, str) or not self.name:
            raise ValueError("LTS name must be a non-empty string")
        if not isinstance(self.initial, str):
            raise ValueError("initial state must be a string")
        normalized: dict[str, list[tuple[str, str]]] = {}
        for src, edges in self.transitions.items():
            if not isinstance(src, str):
                raise ValueError("transition source states must be strings")
            if not isinstance(edges, list):
                raise ValueError(f"transitions[{src!r}] must be a list of [label, target] pairs")
            seen: set[tuple[str, str]] = set()
            out: list[tuple[str, str]] = []
            for edge in edges:
                if not isinstance(edge, list) or len(edge) != 2:
                    raise ValueError(f"transition {edge!r} must be a [label, target] pair")
                label, dst = edge
                if not isinstance(label, str) or not label:
                    raise ValueError(f"action labels must be non-empty strings, got {label!r}")
                if not isinstance(dst, str):
                    raise ValueError(f"transition targets must be strings, got {dst!r}")
                if (label, dst) not in seen:
                    seen.add((label, dst))
                    out.append((label, dst))
            normalized[src] = out
        self.transitions = normalized
        states = self.states()
        if self.initial not in states:
            raise ValueError(
                f"initial state {self.initial!r} is not a state of LTS {self.name!r}; "
                f"known states: {sorted(states)}"
            )

    def states(self) -> set[str]:
        result: set[str] = set(self.transitions)
        for edges in self.transitions.values():
            for _, dst in edges:
                result.add(dst)
        return result

    def labels(self) -> set[str]:
        return {label for edges in self.transitions.values() for label, _ in edges}

    def visible_labels(self) -> set[str]:
        return self.labels() - {TAU}

    def successors(self, state: str, label: str | None = None) -> list[tuple[str, str]]:
        edges = self.transitions.get(state, [])
        if label is None:
            return list(edges)
        return [(lab, dst) for lab, dst in edges if lab == label]

    def tau_successors(self, state: str) -> list[str]:
        return [dst for lab, dst in self.transitions.get(state, []) if lab == TAU]

    def is_deadlock(self, state: str) -> bool:
        return not self.transitions.get(state)

    def to_dict(self) -> dict:
        return {
            "name": self.name,
            "initial": self.initial,
            "transitions": {
                src: [[label, dst] for label, dst in edges]
                for src, edges in sorted(self.transitions.items())
            },
        }

    @classmethod
    def from_dict(cls, data: dict) -> "LTS":
        if not isinstance(data, dict):
            raise ValueError("LTS model must be a JSON object")
        try:
            return cls(
                name=data["name"],
                initial=data["initial"],
                transitions=data.get("transitions", {}),
            )
        except KeyError as exc:
            raise ValueError(f"LTS model missing required field: {exc.args[0]!r}") from None

    @classmethod
    def load(cls, path: str | Path) -> "LTS":
        with open(path, "r", encoding="utf-8") as handle:
            return cls.from_dict(json.load(handle))

    def dump(self, path: str | Path) -> None:
        with open(path, "w", encoding="utf-8") as handle:
            json.dump(self.to_dict(), handle, ensure_ascii=False, indent=2)
            handle.write("\n")


def all_states(models: Iterable[LTS]) -> set[str]:
    result: set[str] = set()
    for model in models:
        result |= model.states()
    return result
