"""Types, schemes and provenance metadata for Hindley-Milner inference."""
from __future__ import annotations

from dataclasses import dataclass

from .source import Span


@dataclass(frozen=True)
class Origin:
    """Where a type came from (a source span plus an explanation)."""
    span: Span
    message: str


@dataclass(frozen=True)
class Reason:
    """Why two types had to be equal (a constraint)."""
    span: Span
    message: str


class Type:
    __slots__ = ("origin", "links")

    def __init__(self, origin: Origin | None = None):
        self.origin = origin
        # Provenance graph edges: (other_type, reason) added by unification.
        self.links: list[tuple[Type, Reason]] = []


class TVar(Type):
    __slots__ = ("id", "instance")
    _next_id = 0

    def __init__(self, origin: Origin | None = None):
        super().__init__(origin)
        self.id = TVar._next_id
        TVar._next_id += 1
        self.instance: Type | None = None


class TCon(Type):
    __slots__ = ("name", "args")

    def __init__(self, name: str, args=(), origin: Origin | None = None):
        super().__init__(origin)
        self.name = name
        self.args: list[Type] = list(args)


def t_int(origin: Origin | None = None) -> TCon:
    return TCon("Int", (), origin)


def t_bool(origin: Origin | None = None) -> TCon:
    return TCon("Bool", (), origin)


def t_fun(arg: Type, res: Type, origin: Origin | None = None) -> TCon:
    return TCon("->", (arg, res), origin)


@dataclass
class Scheme:
    """A polymorphic type: quantified variables plus a type."""
    vars: list
    typ: Type


def prune(t: Type) -> Type:
    """Follow bound type variables to the type they stand for."""
    while isinstance(t, TVar) and t.instance is not None:
        t = t.instance
    return t


def free_type_vars(t: Type, out: dict | None = None) -> dict:
    if out is None:
        out = {}
    t = prune(t)
    if isinstance(t, TVar):
        out[t.id] = t
    elif isinstance(t, TCon):
        for arg in t.args:
            free_type_vars(arg, out)
    return out


def show(t: Type, names: dict | None = None) -> str:
    """Render a type using letters a, b, c ... for variables."""
    if names is None:
        names = {}
    t = prune(t)
    if isinstance(t, TVar):
        if t.id not in names:
            names[t.id] = _var_name(len(names))
        return names[t.id]
    if t.name == "->":
        left = show(t.args[0], names)
        left_t = prune(t.args[0])
        if isinstance(left_t, TCon) and left_t.name == "->":
            left = f"({left})"
        return f"{left} -> {show(t.args[1], names)}"
    if t.args:
        return f"{t.name} {' '.join(show(a, names) for a in t.args)}"
    return t.name


def _var_name(i: int) -> str:
    letters = "abcdefghijklmnopqrstuvwxyz"
    if i < 26:
        return letters[i]
    return f"{letters[i % 26]}{i // 26}"
