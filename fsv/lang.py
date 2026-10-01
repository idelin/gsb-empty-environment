"""A small restricted imperative language: assignments, conditionals, loops.

Programs are tuples of statements.  Statements carry an explicit source
location (`loc`); the builder helpers below capture the caller's file and
line automatically via `inspect`, so every step of execution can be mapped
back to a genuine source position.
"""

import inspect
import os
from dataclasses import dataclass


# ---------------------------------------------------------------------------
# Expressions
# ---------------------------------------------------------------------------

class Expr:
    __slots__ = ()


@dataclass(frozen=True)
class Lit(Expr):
    value: int


@dataclass(frozen=True)
class Var(Expr):
    name: str


@dataclass(frozen=True)
class Bin(Expr):
    op: str  # + - * // % == != < <= > >= and or
    left: Expr
    right: Expr


@dataclass(frozen=True)
class Un(Expr):
    op: str  # "neg" | "not"
    e: Expr


@dataclass(frozen=True)
class Idx(Expr):
    arr: Expr
    index: Expr


@dataclass(frozen=True)
class Len(Expr):
    arr: Expr


# ---------------------------------------------------------------------------
# Statements
# ---------------------------------------------------------------------------

class Stmt:
    __slots__ = ()
    loc: str


@dataclass(frozen=True)
class Assign(Stmt):
    name: str
    expr: Expr
    loc: str = "?"


@dataclass(frozen=True)
class NewArr(Stmt):
    """name := new int[size]  (finite-heap allocation, cells zeroed)."""

    name: str
    size: Expr
    loc: str = "?"


@dataclass(frozen=True)
class Store(Stmt):
    """arr[index] := value"""

    arr: Expr
    index: Expr
    value: Expr
    loc: str = "?"


@dataclass(frozen=True)
class If(Stmt):
    cond: Expr
    then: tuple
    orelse: tuple
    loc: str = "?"


@dataclass(frozen=True)
class While(Stmt):
    cond: Expr
    body: tuple
    loc: str = "?"


@dataclass(frozen=True)
class Try(Stmt):
    """try: body  finally: final   (faults are not catchable, only cleanup)."""

    body: tuple
    final: tuple
    loc: str = "?"


@dataclass(frozen=True)
class Assert(Stmt):
    cond: Expr
    loc: str = "?"


# ---------------------------------------------------------------------------
# Builders with automatic source-location capture
# ---------------------------------------------------------------------------

def _srcloc():
    frame = inspect.currentframe()
    try:
        caller = frame.f_back.f_back
        return f"{os.path.basename(caller.f_code.co_filename)}:{caller.f_lineno}"
    finally:
        del frame


def _e(x):
    if isinstance(x, Expr):
        return x
    if isinstance(x, bool):
        return Lit(int(x))
    if isinstance(x, int):
        return Lit(x)
    if isinstance(x, str):
        return Var(x)
    raise TypeError(f"cannot use {x!r} as an expression")


def _block(x):
    if isinstance(x, Stmt):
        return (x,)
    return tuple(x)


def lit(v):
    return Lit(v)


def var(name):
    return Var(name)


def bin(op, left, right):
    return Bin(op, _e(left), _e(right))


def neg(e):
    return Un("neg", _e(e))


def not_(e):
    return Un("not", _e(e))


def idx(arr, index):
    return Idx(_e(arr), _e(index))


def arrlen(arr):
    return Len(_e(arr))


def assign(name, expr, loc=None):
    return Assign(name, _e(expr), loc or _srcloc())


def newarr(name, size, loc=None):
    return NewArr(name, _e(size), loc or _srcloc())


def store(arr, index, value, loc=None):
    return Store(_e(arr), _e(index), _e(value), loc or _srcloc())


def iff(cond, then, orelse=(), loc=None):
    return If(_e(cond), _block(then), _block(orelse), loc or _srcloc())


def while_(cond, body, loc=None):
    return While(_e(cond), _block(body), loc or _srcloc())


def try_(body, final, loc=None):
    return Try(_block(body), _block(final), loc or _srcloc())


def assert_(cond, loc=None):
    return Assert(_e(cond), loc or _srcloc())
