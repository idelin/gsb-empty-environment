"""Ordered DAG of labelled, attributed nodes with shared subtrees.

Nodes are immutable and content-addressed (:attr:`Node.key`), so a subtree
referenced by several parents is stored once and evaluated once.  A DAG is
acyclic: structural cycles (including self loops) are rejected up front with a
concrete witness path.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Iterable, Mapping, Sequence


class DagError(ValueError):
    """Base error for malformed DAG input."""


class StructuralCycleError(DagError):
    """Raised when the input edges contain a directed structural cycle."""

    def __init__(self, path: tuple[int, ...]):
        self.path = path
        arrow = " -> ".join(str(n) for n in path)
        super().__init__(f"structural cycle detected: {arrow}")


def _freeze_attrs(attrs: Mapping[str, Any] | None) -> tuple[tuple[str, Any], ...]:
    if not attrs:
        return ()
    return tuple(sorted(attrs.items(), key=lambda kv: kv[0]))


@dataclass(frozen=True, slots=True)
class Node:
    label: str
    children: tuple[int, ...]
    attrs: tuple[tuple[str, Any], ...] = ()

    @property
    def arity(self) -> int:
        return len(self.children)

    def attr_map(self) -> dict[str, Any]:
        return dict(self.attrs)

    @property
    def key(self) -> tuple:
        return (self.label, self.children, self.attrs)


class Dag:
    """A finite ordered, acyclic directed graph of :class:`Node` values."""

    def __init__(
        self,
        alphabet: Iterable[str] | None = None,
        nodes: Mapping[int, Node] | None = None,
        *,
        _cons: dict[tuple, int] | None = None,
        _next_id: int | None = None,
    ) -> None:
        self.alphabet: frozenset[str] = frozenset(alphabet or ())
        self.nodes: dict[int, Node] = dict(nodes or {})
        self._cons: dict[tuple, int] = dict(_cons or {})
        self._next_id: int = _next_id if _next_id is not None else self._auto_next_id()
        if self.alphabet:
            self._validate_labels()
        self._parents: dict[int, set[int]] | None = None
        if self.nodes:
            self._check_acyclic()
            if not self._cons:
                self._build_cons()

    # ------------------------------------------------------------------ basic
    def _auto_next_id(self) -> int:
        return (max(self.nodes) + 1) if self.nodes else 0

    def _validate_labels(self) -> None:
        bad = sorted({n.label for n in self.nodes.values() if n.label not in self.alphabet})
        if bad:
            raise DagError(f"labels not in alphabet: {bad}")

    def __contains__(self, node_id: int) -> bool:
        return node_id in self.nodes

    def __len__(self) -> int:
        return len(self.nodes)

    def node(self, node_id: int) -> Node:
        return self.nodes[node_id]

    def attrs(self, node_id: int) -> dict[str, Any]:
        return self.nodes[node_id].attr_map()

    def ids(self) -> tuple[int, ...]:
        return tuple(self.nodes)

    def parents(self, node_id: int) -> frozenset[int]:
        if self._parents is None:
            self._parents = {i: set() for i in self.nodes}
            for pid, pn in self.nodes.items():
                for ch in pn.children:
                    self._parents[ch].add(pid)
        return frozenset(self._parents.get(node_id, ()))

    def roots(self) -> tuple[int, ...]:
        return tuple(i for i in self.nodes if not self.parents(i))

    # ------------------------------------------------------------ validation
    def _check_acyclic(self) -> None:
        missing = [ch for n in self.nodes.values() for ch in n.children if ch not in self.nodes]
        if missing:
            raise DagError(f"dangling child references: {sorted(set(missing))}")
        color: dict[int, int] = {}
        stack: list[int] = []

        def visit(vid: int) -> None:
            color[vid] = 1
            stack.append(vid)
            for ch in self.nodes[vid].children:
                c = color.get(ch, 0)
                if c == 1:
                    start = stack.index(ch)
                    raise StructuralCycleError(tuple(stack[start:] + [ch]))
                if c == 0:
                    visit(ch)
            stack.pop()
            color[vid] = 2

        for vid in self.nodes:
            if color.get(vid, 0) == 0:
                visit(vid)

    def _find_cycle(self) -> tuple[int, ...]:
        color: dict[int, int] = {}
        stack: list[int] = []

        def visit(vid: int) -> tuple[int, ...] | None:
            color[vid] = 1
            stack.append(vid)
            for ch in self.nodes[vid].children:
                c = color.get(ch, 0)
                if c == 1:
                    return tuple(stack[stack.index(ch):] + [ch])
                if c == 0:
                    found = visit(ch)
                    if found:
                        return found
            stack.pop()
            color[vid] = 2
            return None

        for vid in self.nodes:
            if color.get(vid, 0) == 0:
                found = visit(vid)
                if found:
                    return found
        return ()

    def _build_cons(self) -> None:
        self._cons = {}
        for nid, node in self.nodes.items():
            self._cons.setdefault(node.key, nid)
        self._next_id = max(self.nodes) + 1

    def topo_order(self) -> tuple[int, ...]:
        """Children before parents (Kahn on reversed edges); deterministic."""
        indeg = {i: self.nodes[i].arity for i in self.nodes}
        queue = sorted(i for i, d in indeg.items() if d == 0)
        order: list[int] = []
        parent_count: dict[int, list[int]] = {i: [] for i in self.nodes}
        for pid, pn in self.nodes.items():
            for ch in pn.children:
                parent_count[ch].append(pid)
        head = 0
        while head < len(queue):
            vid = queue[head]
            head += 1
            order.append(vid)
            for pid in sorted(parent_count[vid]):
                indeg[pid] -= 1
                if indeg[pid] == 0:
                    queue.append(pid)
        if len(order) != len(self.nodes):
            raise StructuralCycleError(self._find_cycle())
        return tuple(order)

    # ------------------------------------------------------------ hash-cons
    def intern(
        self,
        label: str,
        children: Sequence[int] = (),
        attrs: Mapping[str, Any] | None = None,
    ) -> int:
        """Return the id of an (existing or new) structurally equal node."""
        node = Node(label, tuple(children), _freeze_attrs(attrs))
        if self.alphabet and label not in self.alphabet:
            raise DagError(f"label {label!r} not in alphabet")
        for ch in children:
            if ch not in self.nodes:
                raise DagError(f"unknown child id {ch}")
        existing = self._cons.get(node.key)
        if existing is not None:
            return existing
        nid = self._next_id
        self._next_id += 1
        self.nodes[nid] = node
        self._cons[node.key] = nid
        self._parents = None
        return nid

    def clone(self) -> "Dag":
        return Dag(
            self.alphabet,
            self.nodes,
            _cons=dict(self._cons),
            _next_id=self._next_id,
        )

    # ------------------------------------------------------------ construct
    @staticmethod
    def from_adjacency(
        alphabet: Iterable[str],
        raw_nodes: Iterable[Any],
    ) -> "Dag":
        """Build a DAG from explicit ``(id, label, children[, attrs])`` rows.

        The alphabet and every edge are supplied explicitly; no parsing or
        automaton library is involved.
        """
        nodes: dict[int, Node] = {}
        for row in raw_nodes:
            if not isinstance(row, (tuple, list)) or len(row) not in (3, 4):
                raise DagError(f"raw node must be (id,label,children[,attrs]): {row!r}")
            nid, label, children = row[0], row[1], row[2]
            attrs = row[3] if len(row) == 4 else None
            if not isinstance(children, (tuple, list)):
                raise DagError("children must be a tuple/list")
            if nid in nodes:
                raise DagError(f"duplicate node id {nid}")
            nodes[nid] = Node(str(label), tuple(children), _freeze_attrs(attrs))
        dag = Dag.__new__(Dag)
        dag.alphabet = frozenset(alphabet)
        dag.nodes = nodes
        dag._parents = None
        dag._check_acyclic()
        dag._validate_labels()
        dag._build_cons()
        return dag
