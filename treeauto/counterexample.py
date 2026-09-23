"""Serializable, independently replayable counterexamples.

A counterexample captures: the explicit alphabet, the transition rules, the
constraints, the (shared) tree, and an acceptance expectation that the
automaton violates. It serializes to plain JSON and can be replayed by
``samples/counterexample_runner.py`` without any of the caller's state.
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Mapping, Optional

from .core import (Automaton, DEFAULT_PREDICATES, Node, PredicateRegistry,
                   StructuralCycleError, postorder_unique)
from .evaluation import RunResult, evaluate


def tree_to_spec(root: Node) -> dict:
    """Serialize a (possibly shared) DAG with explicit node ids."""
    nodes = postorder_unique(root)
    ids = {n: i for i, n in enumerate(nodes)}
    return {
        "root": ids[root],
        "nodes": [
            {"label": n.label, "attrs": n.attrs_dict(),
             "children": [ids[c] for c in n.children]}
            for n in nodes
        ],
    }


def tree_from_spec(spec: Mapping) -> Node:
    memo = {}
    visiting = set()

    def build(index: int) -> Node:
        if index in memo:
            return memo[index]
        if index in visiting:
            raise StructuralCycleError(
                f"tree spec contains a structural cycle at node {index}")
        visiting.add(index)
        data = spec["nodes"][index]
        node = Node.make(data["label"],
                         [build(c) for c in data["children"]],
                         data.get("attrs"))
        visiting.discard(index)
        memo[index] = node
        return node

    return build(spec["root"])


@dataclass
class ReplayResult:
    expected_accepted: bool
    actual_accepted: bool
    confirmed: bool          # True if the expectation is still violated
    run: RunResult


@dataclass
class Counterexample:
    automaton_spec: dict
    tree_spec: dict
    expected_accepted: bool
    note: str = ""

    def replay(self,
               registry: PredicateRegistry = DEFAULT_PREDICATES) -> ReplayResult:
        automaton = Automaton.from_spec(self.automaton_spec, registry)
        root = tree_from_spec(self.tree_spec)
        run = evaluate(automaton, root)
        return ReplayResult(
            expected_accepted=self.expected_accepted,
            actual_accepted=run.accepted,
            confirmed=(run.accepted != self.expected_accepted),
            run=run)

    def to_json(self) -> str:
        return json.dumps({
            "automaton": self.automaton_spec,
            "tree": self.tree_spec,
            "expected_accepted": self.expected_accepted,
            "note": self.note,
        }, indent=2, sort_keys=True)

    @staticmethod
    def from_json(payload: str) -> "Counterexample":
        data = json.loads(payload)
        return Counterexample(
            automaton_spec=data["automaton"],
            tree_spec=data["tree"],
            expected_accepted=bool(data["expected_accepted"]),
            note=data.get("note", ""))


def check_expectation(automaton: Automaton, root: Node,
                      expected_accepted: bool,
                      note: str = "") -> Optional[Counterexample]:
    """Return a Counterexample iff the automaton disagrees with the
    expectation on this tree, else None."""
    run = evaluate(automaton, root)
    if run.accepted == expected_accepted:
        return None
    return Counterexample(automaton.to_spec(), tree_to_spec(root),
                          expected_accepted, note)
