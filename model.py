"""AST for the finite handle-state model.

A program is a set of functions (one of them named "main") operating on
handle-typed variables.  Conditions are nondeterministic: the verifier
explores both branches of If and every unrolling of While, which keeps the
model finite-state and fully enumerable.
"""
from __future__ import annotations

from dataclasses import dataclass


class Stmt:
    pass


@dataclass(frozen=True)
class Open(Stmt):
    var: str                      # var := fresh handle (resource opened)


@dataclass(frozen=True)
class Close(Stmt):
    var: str                      # close the handle held by var


@dataclass(frozen=True)
class BorrowShared(Stmt):
    owner: str                    # shared (read-only) borrow of owner's handle
    borrower: str


@dataclass(frozen=True)
class BorrowMut(Stmt):
    owner: str                    # exclusive (read-write) borrow
    borrower: str


@dataclass(frozen=True)
class EndBorrow(Stmt):
    borrower: str                 # return a borrow, restoring the owner


@dataclass(frozen=True)
class Read(Stmt):
    var: str                      # read through var


@dataclass(frozen=True)
class Write(Stmt):
    var: str                      # write through var


@dataclass(frozen=True)
class Move(Stmt):
    dst: str                      # transfer ownership src -> dst
    src: str


@dataclass(frozen=True)
class Alias(Stmt):
    dst: str                      # dst becomes an alias of src (same identity)
    src: str


@dataclass(frozen=True)
class If(Stmt):
    then: tuple
    otherwise: tuple


@dataclass(frozen=True)
class While(Stmt):
    body: tuple


@dataclass(frozen=True)
class Call(Stmt):
    func: str
    args: tuple = ()
    ret: str | None = None        # variable receiving the returned handle


@dataclass(frozen=True)
class Return(Stmt):
    var: str | None = None


@dataclass(frozen=True)
class Function:
    name: str
    params: tuple
    body: tuple


@dataclass(frozen=True)
class Program:
    functions: tuple


def program(main_body, *funcs):
    """Build a Program from a main-body statement list plus extra functions."""
    return Program((Function("main", (), tuple(main_body)),) + tuple(funcs))
