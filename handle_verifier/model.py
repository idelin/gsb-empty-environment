"""Instruction model for the handle-state verifier.

A program is a set of functions over a tiny structured IR.  Handles are
created by ``Open`` and may be aliased (``Assign``), borrowed (``Borrow`` /
``EndBorrow``), used (``Use``), closed (``Close``) or have their ownership
transferred away (``Move`` / ``Call``).  ``If`` and ``While`` are
non-deterministic: the analysis explores every branch, which is what makes
the reachable-state space finite and enumerable.
"""
from __future__ import annotations

from dataclasses import dataclass

# Borrow modes.
SHARED = "shared"
EXCLUSIVE = "exclusive"

# Access kinds.
READ = "read"
WRITE = "write"


class Stmt:
    """Base class for all statements."""
    __slots__ = ()


@dataclass(frozen=True)
class Open(Stmt):
    """var := <fresh resource in state open>."""
    var: str


@dataclass(frozen=True)
class Assign(Stmt):
    """dst aliases the exact same resource as src (shared identity)."""
    dst: str
    src: str


@dataclass(frozen=True)
class Borrow(Stmt):
    """dst := borrow of the resource behind src with the given mode."""
    dst: str
    src: str
    mode: str = SHARED


@dataclass(frozen=True)
class EndBorrow(Stmt):
    """End the borrow held by var; the resource returns to its prior state."""
    var: str


@dataclass(frozen=True)
class Close(Stmt):
    """Close the resource behind var."""
    var: str


@dataclass(frozen=True)
class Use(Stmt):
    """Read or write the resource behind var (owner alias or borrow handle)."""
    var: str
    access: str = READ


@dataclass(frozen=True)
class Move(Stmt):
    """Transfer ownership of the resource behind var out of this function."""
    var: str


@dataclass(frozen=True)
class If(Stmt):
    """Non-deterministic branch; both bodies are explored."""
    then_body: tuple = ()
    else_body: tuple = ()


@dataclass(frozen=True)
class While(Stmt):
    """Non-deterministic loop; the body may run any number of times."""
    body: tuple = ()


@dataclass(frozen=True)
class Call(Stmt):
    """ret := call func(args...) -- arguments are moved into the callee."""
    ret: str | None
    func: str
    args: tuple = ()


@dataclass(frozen=True)
class Return(Stmt):
    """Return ownership of var's resource to the caller (None: keep nothing)."""
    var: str | None = None


@dataclass(frozen=True)
class Function:
    name: str
    params: tuple = ()
    body: tuple = ()


@dataclass(frozen=True)
class Program:
    functions: dict = None
    main: str = "main"

    def __post_init__(self):
        if self.functions is None:
            object.__setattr__(self, "functions", {})
