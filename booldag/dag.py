"""Incremental boolean-expression DAG over rational interval sets.

Nodes
-----
* :class:`Leaf` holds a concrete :class:`IntervalSet`, the empty set, or the
  distinguished UNKNOWN state (``value is None``).  Leaves can be mutated
  (set / add / subtract / clear / set_unknown) and corrected at any time.
* :class:`OpNode` applies ``union`` / ``intersect`` / ``diff`` to its parents.

Incremental maintenance
-----------------------
A leaf mutation dirties only its descendants; re-evaluation walks the dirty
cone in topological order.  Nodes whose dependencies did not change are never
recomputed (``eval_count`` exposes this for tests).

Provenance
----------
Every node keeps ``pieces``: a tuple of ``(interval, frozenset(leaf_names))``
meaning "this interval of the node's value is witnessed by those leaves".
A leaf set S *proves* a region if the pieces whose witness set is a subset of
S already cover it; each piece is greedily minimized under that sound rule.
Because witness sets are sets (not multisets), a leaf reaching a node through
several DAG paths is counted once and retracted once.

Transactions / snapshots
------------------------
``with graph.transaction():`` batches leaf edits into one propagation; an
exception or ``tx.rollback()`` restores prior leaf states and re-evaluates
only the affected cone.  ``graph.snapshot()`` freezes every node's value and
pieces; snapshots are immutable and isolated from later mutations.
"""

from __future__ import annotations

from fractions import Fraction
from typing import Dict, FrozenSet, Iterable, Iterator, Optional, Set, Tuple, Union

from .intervals import Interval, IntervalSet, Number, to_fraction

UNKNOWN = None  # node.value is None iff the value is unknown

OPS = ("union", "intersect", "diff")

Piece = Tuple[Interval, FrozenSet[str]]

__all__ = ["UNKNOWN", "CycleError", "Graph", "Leaf", "OpNode", "Snapshot", "Transaction"]


class CycleError(Exception):
    """Raised when an edge would make the expression graph cyclic."""


# ---------------------------------------------------------------------------
# provenance helpers
# ---------------------------------------------------------------------------

def _covers_region(pieces: Iterable[Piece], leaves: Set[str], target: Interval) -> bool:
    """True if pieces witnessed within ``leaves`` already cover ``target``."""
    acc = IntervalSet()
    for iv, ls in pieces:
        if ls <= leaves:
            acc = acc.union(IntervalSet([iv]))
    return IntervalSet([target]).subtract(acc).is_empty()


def _covers_point(pieces: Iterable[Piece], leaves: Set[str], x: Fraction) -> bool:
    return any(ls <= leaves and iv.contains(x) for iv, ls in pieces)


def _refine(pieces: Iterable[Piece]) -> Tuple[Piece, ...]:
    """Greedily minimize each witness set, then merge equal sets."""
    raw = list(pieces)
    out = []
    for iv, ls in raw:
        keep = set(ls)
        for leaf in sorted(ls):
            trial = keep - {leaf}
            if trial and _covers_region(raw, trial, iv):
                keep = trial
        out.append((iv, frozenset(keep)))
    by_set: Dict[FrozenSet[str], IntervalSet] = {}
    for iv, ls in out:
        by_set[ls] = by_set.get(ls, IntervalSet()).union(IntervalSet([iv]))
    merged = [(iv, ls) for ls, st in by_set.items() for iv in st.intervals]
    merged.sort(key=lambda p: (p[0].lo is not None, p[0].lo, p[0].hi is None, p[0].hi))
    return tuple(merged)


def prove_point(pieces: Tuple[Piece, ...], x: Fraction) -> Optional[FrozenSet[str]]:
    """Minimal leaf set proving ``x`` is covered, or None if not covered."""
    containing = [ls for iv, ls in pieces if iv.contains(x)]
    if not containing:
        return None
    keep: Set[str] = set().union(*containing)
    for leaf in sorted(keep):
        trial = keep - {leaf}
        if trial and _covers_point(pieces, trial, x):
            keep = trial
    return frozenset(keep)


# ---------------------------------------------------------------------------
# nodes
# ---------------------------------------------------------------------------

class Node:
    def __init__(self, graph: "Graph", name: str):
        self.graph = graph
        self.name = name
        self.children: Set["OpNode"] = set()  # dependents (downstream)
        self.value: Optional[IntervalSet] = UNKNOWN
        self.pieces: Tuple[Piece, ...] = ()
        self.eval_count = 0  # instrumentation: how often recompute ran

    # -- read API ---------------------------------------------------------
    def is_unknown(self) -> bool:
        return self.value is None

    def is_empty(self) -> bool:
        return self.value is not None and self.value.is_empty()

    def is_connected(self) -> bool:
        return self.value is not None and self.value.is_connected()

    def contains(self, x: Number) -> bool:
        return self.value is not None and self.value.contains(x)

    def prove(self, x: Number) -> Optional[FrozenSet[str]]:
        return self.graph.prove(self, x)

    def explain(self) -> Tuple[Piece, ...]:
        return self.pieces

    def recompute(self) -> None:
        raise NotImplementedError

    def __repr__(self) -> str:
        return f"<{type(self).__name__} {self.name} value={self.value!r}>"


class Leaf(Node):
    """Mutable source of intervals; UNKNOWN until first set."""

    def __init__(self, graph: "Graph", name: str):
        super().__init__(graph, name)
        self._state: Optional[IntervalSet] = None  # None = unknown

    @property
    def state(self) -> Optional[IntervalSet]:
        return self._state

    # -- mutations --------------------------------------------------------
    def set(self, *intervals: Interval) -> None:
        self._mutate(IntervalSet(intervals))

    def set_set(self, value: IntervalSet) -> None:
        self._mutate(value)

    def add(self, *intervals: Interval) -> None:
        base = self._state if self._state is not None else IntervalSet()
        self._mutate(base.union(IntervalSet(intervals)))

    def subtract(self, *intervals: Interval) -> None:
        base = self._state if self._state is not None else IntervalSet()
        self._mutate(base.subtract(IntervalSet(intervals)))

    def clear(self) -> None:
        """Retract everything this leaf contributed (defined empty set)."""
        self._mutate(IntervalSet())

    def set_unknown(self) -> None:
        self._mutate(None)

    def _mutate(self, state: Optional[IntervalSet]) -> None:
        tx = self.graph._tx
        if tx is not None:
            if self not in tx.log:
                tx.log[self] = self._state
            self._state = state
            tx.dirty.add(self)
        else:
            self._state = state
            self.graph._propagate({self})

    # -- evaluation -------------------------------------------------------
    def recompute(self) -> None:
        self.eval_count += 1
        self.value = self._state
        if self._state is None:
            self.pieces = ()
        else:
            self.pieces = tuple(
                (iv, frozenset({self.name})) for iv in self._state.intervals
            )


class OpNode(Node):
    def __init__(self, graph: "Graph", name: str, op: str, parents: Tuple[Node, ...]):
        super().__init__(graph, name)
        if op not in OPS:
            raise ValueError(f"unknown op {op!r}; expected one of {OPS}")
        if op == "diff" and len(parents) != 2:
            raise ValueError("diff takes exactly two parents")
        if not parents:
            raise ValueError("op node needs at least one parent")
        self.op = op
        self.parents: Tuple[Node, ...] = parents
        for p in parents:
            p.children.add(self)

    def recompute(self) -> None:
        self.eval_count += 1
        vals = [p.value for p in self.parents]
        if any(v is None for v in vals):
            self.value = UNKNOWN
            self.pieces = ()
            return
        if self.op == "union":
            value = IntervalSet()
            for v in vals:
                value = value.union(v)
            raw = [pc for p in self.parents for pc in p.pieces]
        elif self.op == "intersect":
            value = vals[0]
            pieces = list(self.parents[0].pieces)
            for p in self.parents[1:]:
                value = value.intersect(p.value)
                pieces = [
                    (iv, sa | sb)
                    for (ia, sa) in pieces
                    for (ib, sb) in p.pieces
                    for iv in IntervalSet([ia]).intersect(IntervalSet([ib])).intervals
                ]
            raw = pieces
        else:  # diff
            a, b = self.parents
            value = a.value.subtract(b.value)
            raw = [
                (iv, sa)
                for (ia, sa) in a.pieces
                for iv in IntervalSet([ia]).subtract(b.value).intervals
            ]
        self.value = value
        self.pieces = _refine(raw)


# ---------------------------------------------------------------------------
# transaction / snapshot
# ---------------------------------------------------------------------------

class Transaction:
    """Batch leaf mutations; single propagation on exit, rollback on error."""

    def __init__(self, graph: "Graph"):
        self.graph = graph
        self.log: Dict[Leaf, Optional[IntervalSet]] = {}
        self.dirty: Set[Leaf] = set()
        self._rolled_back = False

    def __enter__(self) -> "Transaction":
        if self.graph._tx is not None:
            raise RuntimeError("nested transactions are not supported")
        self.graph._tx = self
        return self

    def rollback(self) -> None:
        """Mark this transaction to be undone when the with-block exits."""
        self._rolled_back = True

    def __exit__(self, exc_type, exc, tb) -> bool:
        self.graph._tx = None
        if exc_type is not None or self._rolled_back:
            for leaf, state in self.log.items():
                leaf._state = state
        if self.dirty:
            self.graph._propagate(set(self.dirty))
        return False  # never suppress


class Snapshot:
    """Frozen view of every node's value and provenance; immune to later edits."""

    def __init__(self, graph: "Graph"):
        self._data: Dict[str, Tuple[Optional[IntervalSet], Tuple[Piece, ...]]] = {
            name: (n.value, n.pieces) for name, n in graph.nodes.items()
        }

    def value(self, node: Union[str, Node]) -> Optional[IntervalSet]:
        name = node if isinstance(node, str) else node.name
        return self._data[name][0]

    def is_unknown(self, node: Union[str, Node]) -> bool:
        return self.value(node) is None

    def prove(self, node: Union[str, Node], x: Number) -> Optional[FrozenSet[str]]:
        name = node if isinstance(node, str) else node.name
        value, pieces = self._data[name]
        if value is None or not value.contains(to_fraction(x)):
            return None
        return prove_point(pieces, to_fraction(x))


# ---------------------------------------------------------------------------
# graph
# ---------------------------------------------------------------------------

class Graph:
    def __init__(self):
        self.nodes: Dict[str, Node] = {}
        self._tx: Optional[Transaction] = None

    # -- construction -----------------------------------------------------
    def leaf(self, name: str) -> Leaf:
        return self._add(Leaf(self, name))

    def node(self, name: str, op: str, parents: Iterable[Union[str, Node]]) -> OpNode:
        resolved = tuple(self._resolve(p) for p in parents)
        return self._add(OpNode(self, name, op, resolved))

    def _add(self, node: Node):
        if node.name in self.nodes:
            raise ValueError(f"duplicate node name {node.name!r}")
        self.nodes[node.name] = node
        self._propagate({node})
        return node

    def _resolve(self, ref: Union[str, Node]) -> Node:
        if isinstance(ref, Node):
            return ref
        return self.nodes[ref]

    # -- structural edits ---------------------------------------------------
    def add_parent(self, node: Union[str, OpNode], parent: Union[str, Node]) -> None:
        """Attach an extra parent, rejecting edges that would create a cycle."""
        node = self._resolve(node)
        parent = self._resolve(parent)
        if not isinstance(node, OpNode):
            raise TypeError("only op nodes accept parents")
        if parent is node or self._is_ancestor(node, parent):
            raise CycleError(f"edge {parent.name} -> {node.name} would create a cycle")
        if parent in node.parents:
            return
        if node.op == "diff":
            raise ValueError("diff takes exactly two parents")
        node.parents = node.parents + (parent,)
        parent.children.add(node)
        self._propagate({node})

    def remove_leaf(self, leaf: Union[str, Leaf]) -> None:
        """Delete a leaf; its former dependents are re-evaluated without it."""
        leaf = self._resolve(leaf)
        if not isinstance(leaf, Leaf):
            raise TypeError("only leaves can be removed")
        if self._tx is not None:
            raise RuntimeError("remove_leaf outside transactions only")
        dependents = list(leaf.children)
        for child in dependents:
            child.parents = tuple(p for p in child.parents if p is not leaf)
            leaf.children.discard(child)
        del self.nodes[leaf.name]
        if dependents:
            self._propagate(set(dependents))

    def _is_ancestor(self, maybe_ancestor: Node, node: Node) -> bool:
        stack = [node]
        seen = set()
        while stack:
            cur = stack.pop()
            if cur is maybe_ancestor:
                return True
            if cur in seen:
                continue
            seen.add(cur)
            if isinstance(cur, OpNode):
                stack.extend(cur.parents)
        return False

    # -- incremental evaluation --------------------------------------------
    def _propagate(self, roots: Set[Node]) -> None:
        dirty: Set[Node] = set()
        stack = list(roots)
        while stack:
            n = stack.pop()
            if n in dirty:
                continue
            dirty.add(n)
            stack.extend(n.children)
        done: Set[Node] = set()

        def visit(n: Node) -> None:
            if n in done:
                return
            if isinstance(n, OpNode):
                for p in n.parents:
                    if p in dirty:
                        visit(p)
            n.recompute()
            done.add(n)

        for n in list(dirty):
            visit(n)

    # -- queries ------------------------------------------------------------
    def value(self, node: Union[str, Node]) -> Optional[IntervalSet]:
        return self._resolve(node).value

    def prove(self, node: Union[str, Node], x: Number) -> Optional[FrozenSet[str]]:
        node = self._resolve(node)
        if node.value is None:
            return None
        return prove_point(node.pieces, to_fraction(x))

    def leaves_of(self, node: Union[str, Node]) -> FrozenSet[str]:
        node = self._resolve(node)
        out: Set[str] = set()
        stack = [node]
        seen: Set[Node] = set()
        while stack:
            cur = stack.pop()
            if cur in seen:
                continue
            seen.add(cur)
            if isinstance(cur, Leaf):
                out.add(cur.name)
            else:
                stack.extend(cur.parents)
        return frozenset(out)

    # -- batching / freezing -------------------------------------------------
    def transaction(self) -> Transaction:
        return Transaction(self)

    def snapshot(self) -> Snapshot:
        return Snapshot(self)
