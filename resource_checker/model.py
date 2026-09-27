"""Finite-state program model.

A program is a labelled control-flow graph.  Nodes carry one operation;
edges are labelled with their kind:

- ``normal``    : ordinary control flow
- ``exception`` : exceptional control flow out of a node
- ``finally``   : edge into a finally / cleanup block
- ``cancel``    : cancellation edge

Nodes that can raise / be cancelled carry ``may_raise`` / ``may_cancel``
flags.  The *abstract* analysis conservatively treats every exception and
cancel edge as feasible; the *concrete replay* uses the flags to decide
whether such an edge can actually be taken.  This gap is what allows
bounded concrete replay to filter abstract false positives.

The verification object is limited to the given finite-state model.
"""
from __future__ import annotations

from dataclasses import dataclass, field

# --- operation kinds -------------------------------------------------------
ACQUIRE = "acquire"          # (var,)            bind a fresh resource to var
RELEASE = "release"          # (var,)            release resource held by var
TRANSFER = "transfer"        # (src, dst)        move ownership src -> dst
BORROW = "borrow"            # (var,)            begin a borrow of var's resource
END_BORROW = "end_borrow"    # (var,)            end all borrows of var's resource
USE = "use"                  # (var,)            use resource held by var
ALIAS = "alias"              # (dst, src)        precise alias: dst := src
MAYBE_ALIAS = "maybe_alias"  # (dst, src)        nondeterministic alias (may/may not)
NOP = "nop"

OP_KINDS = frozenset(
    {ACQUIRE, RELEASE, TRANSFER, BORROW, END_BORROW, USE, ALIAS, MAYBE_ALIAS, NOP}
)

# --- edge kinds ------------------------------------------------------------
NORMAL = "normal"
EXCEPTION = "exception"
FINALLY = "finally"
CANCEL = "cancel"

EDGE_KINDS = frozenset({NORMAL, EXCEPTION, FINALLY, CANCEL})

# --- exit kinds ------------------------------------------------------------
EXIT_NORMAL = "normal"
EXIT_EXCEPTION = "exception"
EXIT_CANCEL = "cancel"


@dataclass(frozen=True)
class Op:
    kind: str
    args: tuple[str, ...] = ()

    def __post_init__(self) -> None:
        if self.kind not in OP_KINDS:
            raise ValueError(f"unknown op kind: {self.kind!r}")


@dataclass(frozen=True)
class Edge:
    kind: str
    target: str

    def __post_init__(self) -> None:
        if self.kind not in EDGE_KINDS:
            raise ValueError(f"unknown edge kind: {self.kind!r}")


@dataclass
class Node:
    name: str
    op: Op = field(default_factory=lambda: Op(NOP))
    edges: tuple[Edge, ...] = ()
    may_raise: bool = True
    may_cancel: bool = True


@dataclass
class Program:
    """A finite-state model: nodes, one entry, and named exit sinks."""

    nodes: dict[str, Node]
    entry: str
    exits: dict[str, str]  # exit node name -> exit kind (normal/exception/cancel)

    def __post_init__(self) -> None:
        if self.entry not in self.nodes:
            raise ValueError(f"entry node {self.entry!r} not in program")
        for name, kind in self.exits.items():
            if name not in self.nodes:
                raise ValueError(f"exit node {name!r} not in program")
            if kind not in (EXIT_NORMAL, EXIT_EXCEPTION, EXIT_CANCEL):
                raise ValueError(f"unknown exit kind: {kind!r}")
        for node in self.nodes.values():
            for edge in node.edges:
                if edge.target not in self.nodes:
                    raise ValueError(
                        f"edge target {edge.target!r} of node {node.name!r} not in program"
                    )

    def exit_kind(self, node_name: str) -> str | None:
        return self.exits.get(node_name)


class Builder:
    """Convenience builder for programs."""

    def __init__(self) -> None:
        self.nodes: dict[str, Node] = {}
        self.exits: dict[str, str] = {}

    def node(
        self,
        name: str,
        op: Op | None = None,
        edges: tuple[Edge, ...] | list[Edge] = (),
        may_raise: bool = True,
        may_cancel: bool = True,
    ) -> "Builder":
        self.nodes[name] = Node(
            name=name,
            op=op if op is not None else Op(NOP),
            edges=tuple(edges),
            may_raise=may_raise,
            may_cancel=may_cancel,
        )
        return self

    def exit(self, name: str, kind: str = EXIT_NORMAL) -> "Builder":
        self.nodes[name] = Node(name=name)
        self.exits[name] = kind
        return self

    def build(self, entry: str) -> Program:
        return Program(nodes=dict(self.nodes), entry=entry, exits=dict(self.exits))
