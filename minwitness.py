"""Minimal witness sets for monotone AND/OR circuits with retractable facts.

A circuit is an acyclic AND/OR graph (sharing allowed). Leaves are facts
that can be asserted or retracted at any time. For every root that is
currently true, the engine reports a minimal witness: a smallest set of
asserted facts that, on its own, forces the root to be true. Ties are
broken by lexicographic order of the sorted fact ids.

The witness is computed globally, not by greedily concatenating per-subtree
local optima. Each node carries a Pareto frontier of witness candidate sets
(an antichain under set inclusion); AND nodes combine frontiers by union,
OR nodes by merging, and dominated sets are pruned. This keeps shared
leaves counted once and finds the true global optimum.

Retracting or asserting a fact only recomputes the values and frontiers of
the affected ancestor subgraph (incremental update).

Python 3.11, standard library only.
"""

from __future__ import annotations

import argparse
import sys
from typing import Iterable, TextIO

Witness = frozenset  # of fact ids (str)


def _witness_key(witness: Witness) -> tuple:
    """Ordering key: fewer facts first, then lexicographic by sorted ids."""
    return (len(witness), tuple(sorted(witness)))


def _prune(candidates: Iterable[Witness]) -> tuple[Witness, ...]:
    """Reduce candidates to the antichain under set inclusion.

    A set A dominates B iff A is a subset of B: anything B can explain,
    A explains with a (non-strictly) smaller, lexicographically no-worse
    footprint in every surrounding context, because gates are monotone.
    """
    unique = {frozenset(c) for c in candidates}
    kept = [
        s for s in unique
        if not any(other < s for other in unique)
    ]
    kept.sort(key=_witness_key)
    return tuple(kept)


class CircuitError(ValueError):
    """Raised for malformed circuit definitions or unknown node ids."""


class Circuit:
    """An acyclic AND/OR circuit over retractable fact leaves."""

    def __init__(self) -> None:
        self._kind: dict[str, str] = {}            # id -> 'leaf' | 'and' | 'or'
        self._children: dict[str, tuple[str, ...]] = {}
        self._parents: dict[str, set[str]] = {}
        self._asserted: dict[str, bool] = {}       # leaves only
        self._depth: dict[str, int] = {}           # topological rank
        self._value: dict[str, bool] = {}
        self._frontier: dict[str, tuple[Witness, ...]] = {}
        self._roots: list[str] = []

    # ------------------------------------------------------------------
    # construction
    # ------------------------------------------------------------------
    def add_leaf(self, node_id: str, asserted: bool = False) -> None:
        self._require_new(node_id)
        self._kind[node_id] = "leaf"
        self._children[node_id] = ()
        self._parents[node_id] = set()
        self._asserted[node_id] = bool(asserted)
        self._depth[node_id] = 0
        self._recompute(node_id)

    def add_gate(self, kind: str, node_id: str, children: Iterable[str]) -> None:
        if kind not in ("and", "or"):
            raise CircuitError(f"unknown gate kind: {kind!r}")
        self._require_new(node_id)
        child_ids = tuple(children)
        if not child_ids:
            raise CircuitError(f"gate {node_id!r} needs at least one child")
        for child in child_ids:
            if child not in self._kind:
                raise CircuitError(f"unknown child node: {child!r}")
        self._kind[node_id] = kind
        self._children[node_id] = child_ids
        self._parents[node_id] = set()
        for child in child_ids:
            self._parents[child].add(node_id)
        self._depth[node_id] = 1 + max(self._depth[c] for c in child_ids)
        self._recompute(node_id)

    def add_root(self, node_id: str) -> None:
        self._require_known(node_id)
        if node_id not in self._roots:
            self._roots.append(node_id)

    # ------------------------------------------------------------------
    # fact toggling (incremental)
    # ------------------------------------------------------------------
    def assert_fact(self, node_id: str) -> None:
        self._set_fact(node_id, True)

    def retract_fact(self, node_id: str) -> None:
        self._set_fact(node_id, False)

    def _set_fact(self, node_id: str, state: bool) -> None:
        self._require_known(node_id)
        if self._kind[node_id] != "leaf":
            raise CircuitError(f"not a fact leaf: {node_id!r}")
        if self._asserted[node_id] == state:
            return
        self._asserted[node_id] = state
        # Incremental update: recompute only the dirty ancestor cone,
        # in topological order so children are refreshed before parents.
        dirty = {node_id}
        stack = [node_id]
        while stack:
            current = stack.pop()
            for parent in self._parents[current]:
                if parent not in dirty:
                    dirty.add(parent)
                    stack.append(parent)
        for node in sorted(dirty, key=self._depth.__getitem__):
            self._recompute(node)

    # ------------------------------------------------------------------
    # queries
    # ------------------------------------------------------------------
    def value(self, node_id: str) -> bool:
        self._require_known(node_id)
        return self._value[node_id]

    def witness(self, node_id: str) -> Witness | None:
        """Globally minimal witness for a node, or None if it is false."""
        self._require_known(node_id)
        frontier = self._frontier[node_id]
        if not frontier:
            return None
        return min(frontier, key=_witness_key)

    def roots(self) -> tuple[str, ...]:
        return tuple(self._roots)

    # ------------------------------------------------------------------
    # internals
    # ------------------------------------------------------------------
    def _recompute(self, node_id: str) -> None:
        kind = self._kind[node_id]
        if kind == "leaf":
            asserted = self._asserted[node_id]
            self._value[node_id] = asserted
            self._frontier[node_id] = (frozenset({node_id}),) if asserted else ()
            return
        child_frontiers = [self._frontier[c] for c in self._children[node_id]]
        if kind == "and":
            self._value[node_id] = all(self._value[c] for c in self._children[node_id])
            if not self._value[node_id]:
                self._frontier[node_id] = ()
                return
            combined = child_frontiers[0]
            for frontier in child_frontiers[1:]:
                combined = _prune(a | b for a in combined for b in frontier)
            self._frontier[node_id] = combined
        else:  # or
            self._value[node_id] = any(self._value[c] for c in self._children[node_id])
            if not self._value[node_id]:
                self._frontier[node_id] = ()
                return
            self._frontier[node_id] = _prune(
                s for frontier in child_frontiers for s in frontier
            )

    def _require_new(self, node_id: str) -> None:
        if node_id in self._kind:
            raise CircuitError(f"duplicate node id: {node_id!r}")

    def _require_known(self, node_id: str) -> None:
        if node_id not in self._kind:
            raise CircuitError(f"unknown node id: {node_id!r}")


# ----------------------------------------------------------------------
# command-line interface
# ----------------------------------------------------------------------
def run_script(lines: Iterable[str], circuit: Circuit, out: TextIO) -> None:
    """Execute script commands, writing query results to ``out``.

    Commands (one per line, '#' starts a comment):
      leaf ID [asserted|retracted]   declare a fact leaf (default retracted)
      and ID CHILD...                define an AND gate over known nodes
      or ID CHILD...                 define an OR gate over known nodes
      assert ID                      assert a fact leaf
      retract ID                     retract a fact leaf
      root ID                        designate a root node
      value ID                       print 'ID true' or 'ID false'
      witness [ID]                   print minimal witness for each root,
                                     or for the given node
    """
    for lineno, raw in enumerate(lines, start=1):
        line = raw.split("#", 1)[0].strip()
        if not line:
            continue
        parts = line.split()
        command, args = parts[0].lower(), parts[1:]
        try:
            if command == "leaf" and len(args) in (1, 2):
                state = len(args) == 2 and args[1] == "asserted"
                if len(args) == 2 and args[1] not in ("asserted", "retracted"):
                    raise CircuitError(f"bad leaf state: {args[1]!r}")
                circuit.add_leaf(args[0], asserted=state)
            elif command in ("and", "or") and len(args) >= 2:
                circuit.add_gate(command, args[0], args[1:])
            elif command == "assert" and len(args) == 1:
                circuit.assert_fact(args[0])
            elif command == "retract" and len(args) == 1:
                circuit.retract_fact(args[0])
            elif command == "root" and len(args) == 1:
                circuit.add_root(args[0])
            elif command == "value" and len(args) == 1:
                print(f"{args[0]} {'true' if circuit.value(args[0]) else 'false'}",
                      file=out)
            elif command == "witness" and len(args) <= 1:
                targets = args if args else list(circuit.roots())
                if not targets:
                    raise CircuitError("no roots designated")
                for target in targets:
                    witness = circuit.witness(target)
                    if witness is None:
                        print(f"{target} false", file=out)
                    else:
                        facts = " ".join(sorted(witness))
                        print(f"{target} true {facts}", file=out)
            else:
                raise CircuitError(f"unknown command or bad arity: {command!r}")
        except CircuitError as exc:
            raise CircuitError(f"line {lineno}: {exc}") from None


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="minwitness",
        description="Minimal fact witnesses for AND/OR circuits "
                    "with retractable facts.",
    )
    parser.add_argument(
        "script",
        nargs="?",
        help="command script file (default: read from stdin)",
    )
    args = parser.parse_args(argv)

    circuit = Circuit()
    try:
        if args.script:
            with open(args.script, "r", encoding="utf-8") as handle:
                run_script(handle, circuit, sys.stdout)
        else:
            run_script(sys.stdin, circuit, sys.stdout)
    except CircuitError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main())
