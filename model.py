"""Finite-state model definitions for the resource-ownership checker.

A program is a finite control-flow graph.  Each node carries one operation
(acquire / release / transfer / borrow / alias / ...) and labelled edges.
Edge kinds model structured and exceptional control flow:

* ``normal``  - ordinary control flow
* ``except``  - taken when the source operation raises
* ``finally`` - cleanup edge belonging to a ``finally`` block
* ``cancel``  - asynchronous cancellation edge

Edges may carry a boolean guard over program variables.  The abstract
analysis deliberately ignores guards (a path-insensitive
over-approximation); the concrete replay honours them, which is exactly
what allows replay to filter abstract false positives.
"""

from __future__ import annotations

from dataclasses import dataclass, field

NORMAL = "normal"
EXCEPT = "except"
FINALLY = "finally"
CANCEL = "cancel"
EXCEPTIONAL_KINDS = frozenset({EXCEPT, FINALLY, CANCEL})


# --- operations -----------------------------------------------------------


@dataclass(frozen=True)
class Acquire:
    res: str
    owner: str

    def __str__(self) -> str:
        return f"acquire({self.res} -> {self.owner})"


@dataclass(frozen=True)
class Release:
    res: str
    who: str  # an owner name, or an alias that resolves to the current owner

    def __str__(self) -> str:
        return f"release({self.res} by {self.who})"


@dataclass(frozen=True)
class Transfer:
    res: str
    src: str
    dst: str

    def __str__(self) -> str:
        return f"transfer({self.res}: {self.src} -> {self.dst})"


@dataclass(frozen=True)
class Borrow:
    res: str
    owner: str
    borrower: str

    def __str__(self) -> str:
        return f"borrow({self.res} by {self.borrower} from {self.owner})"


@dataclass(frozen=True)
class EndBorrow:
    res: str
    borrower: str

    def __str__(self) -> str:
        return f"end_borrow({self.res} by {self.borrower})"


@dataclass(frozen=True)
class Alias:
    res: str
    name: str

    def __str__(self) -> str:
        return f"alias({self.name} = {self.res})"


@dataclass(frozen=True)
class Set:
    var: str
    value: bool

    def __str__(self) -> str:
        return f"set({self.var}={self.value})"


@dataclass(frozen=True)
class Raise:
    def __str__(self) -> str:
        return "raise"


@dataclass(frozen=True)
class Nop:
    def __str__(self) -> str:
        return "nop"


# --- graph ----------------------------------------------------------------


@dataclass(frozen=True)
class Edge:
    target: str
    kind: str = NORMAL
    guard: tuple[str, bool] | None = None

    def __str__(self) -> str:
        g = f" if {self.guard[0]}=={self.guard[1]}" if self.guard else ""
        return f"-[{self.kind}{g}]-> {self.target}"


@dataclass
class Node:
    name: str
    op: object = Nop()
    edges: list[Edge] = field(default_factory=list)
    exit_kind: str | None = None  # "normal" | "exception" | "cancel"

    @property
    def is_exit(self) -> bool:
        return self.exit_kind is not None


@dataclass
class Program:
    name: str
    nodes: dict[str, Node]
    entry: str
    resources: tuple[str, ...]
    owners: tuple[str, ...]
    vars: tuple[str, ...] = ()

    def exits(self) -> list[Node]:
        return [n for n in self.nodes.values() if n.is_exit]


def out_edges(node: Node) -> list[Edge]:
    """Edges traversable after executing ``node.op`` (guards ignored).

    A ``raise`` operation only continues along exceptional edges
    (except / finally / cancel).
    """
    if isinstance(node.op, Raise):
        return [e for e in node.edges if e.kind in EXCEPTIONAL_KINDS]
    return list(node.edges)
