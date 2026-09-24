"""Hash-consed expression DAG nodes, structural utilities, domain analysis.

Node identity (``id``) doubles as the *position* used for error localisation:
source nodes carry ``origin == id``; nodes derived during simplification keep
the ``origin`` of the source node they came from, so an error reported while
evaluating a simplified expression points at the same position as the
independent interpreter running on the original expression.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

OPS = ("const", "var", "neg", "add", "sub", "mul", "div", "cond", "undef")
COMMUTATIVE = ("add", "mul")


@dataclass(frozen=True)
class Node:
    id: int
    op: str
    args: tuple = ()
    value: Fraction | None = None   # const payload
    name: str | None = None         # var payload
    origin: int = -1                # source position this node stems from


class Factory:
    """Hash-consing node factory: structurally identical nodes (with the same
    origin) are shared, giving a DAG with O(1) identity comparison."""

    def __init__(self):
        self._intern: dict = {}
        self.nodes: list[Node] = []
        self.vars_by_name: dict[str, list[Node]] = {}

    def _mk(self, op, args=(), value=None, name=None, origin=None):
        key = (op, tuple(a.id for a in args), value, name,
               "self" if origin is None else origin)
        node = self._intern.get(key)
        if node is not None:
            return node
        nid = len(self.nodes)
        node = Node(nid, op, tuple(args), value, name,
                    nid if origin is None else origin)
        self._intern[key] = node
        self.nodes.append(node)
        if op == "var":
            self.vars_by_name.setdefault(name, []).append(node)
        return node

    # -- constructors (origin=None => fresh source node, origin = own id) --
    def const(self, v, origin=None):
        return self._mk("const", value=Fraction(v), origin=origin)

    def var(self, name, origin=None):
        return self._mk("var", name=name, origin=origin)

    def neg(self, a, origin=None):
        return self._mk("neg", (a,), origin=origin)

    def add(self, a, b, origin=None):
        return self._mk("add", (a, b), origin=origin)

    def sub(self, a, b, origin=None):
        return self._mk("sub", (a, b), origin=origin)

    def mul(self, a, b, origin=None):
        return self._mk("mul", (a, b), origin=origin)

    def div(self, a, b, origin=None):
        return self._mk("div", (a, b), origin=origin)

    def cond(self, c, t, e, origin=None):
        return self._mk("cond", (c, t, e), origin=origin)

    def undef(self, pos):
        """An expression with empty domain; ``pos`` is the source position
        responsible for the failure (e.g. the division-by-zero node)."""
        return self._mk("undef", origin=pos)

    def rebuild(self, src: Node, children) -> Node:
        """Rebuild ``src`` with new (simplified) children, keeping origin."""
        return self._mk(src.op, tuple(children), value=src.value,
                        name=src.name, origin=src.origin)


# ---------------------------------------------------------------- printing

def show(n: Node) -> str:
    op = n.op
    if op == "const":
        return str(n.value)
    if op == "var":
        return n.name
    if op == "undef":
        return f"⊥@{n.origin}"
    if op == "neg":
        return f"(- {show(n.args[0])})"
    if op == "cond":
        return f"(if {show(n.args[0])} {show(n.args[1])} {show(n.args[2])})"
    sym = {"add": "+", "sub": "-", "mul": "*", "div": "/"}[op]
    return f"({sym} {show(n.args[0])} {show(n.args[1])})"


def show_redex(src: Node, cs) -> str:
    """Display ``src`` as if rebuilt with already-simplified children."""
    if src.op == "neg":
        return f"(- {show(cs[0])})"
    if src.op == "cond":
        return f"(if {show(cs[0])} {show(cs[1])} {show(cs[2])})"
    sym = {"add": "+", "sub": "-", "mul": "*", "div": "/"}[src.op]
    return f"({sym} {show(cs[0])} {show(cs[1])})"


# ------------------------------------------------------- structural helpers

def same_expr(a: Node, b: Node) -> bool:
    """Structural equality ignoring origin/position."""
    if a.op != b.op:
        return False
    if a.op == "const":
        return a.value == b.value
    if a.op == "var":
        return a.name == b.name
    if a.op == "undef":
        return True
    return all(same_expr(x, y) for x, y in zip(a.args, b.args))


def canon_key(n: Node):
    """Canonical structural key: commutative operands sorted, origin ignored.
    Two engines that simplify the same expression must produce results with
    equal canon keys regardless of the order bindings were supplied in."""
    if n.op == "const":
        return ("const", n.value)
    if n.op == "var":
        return ("var", n.name)
    if n.op == "undef":
        return ("undef",)
    kids = tuple(canon_key(a) for a in n.args)
    if n.op in COMMUTATIVE:
        kids = tuple(sorted(kids))
    return (n.op,) + kids


def free_vars(n: Node, _seen=None, _out=None) -> set:
    if _seen is None:
        _seen, _out = set(), set()
    if n.id in _seen:
        return _out
    _seen.add(n.id)
    if n.op == "var":
        _out.add(n.name)
    for a in n.args:
        free_vars(a, _seen, _out)
    return _out


# ------------------------------------------------------------ domain analysis

def is_total(n: Node, memo: dict | None = None) -> bool:
    """Conservative, sound check: is ``n`` defined for *every* environment?

    Sound but incomplete: e.g. ``x*x + 1`` is never zero over the rationals
    but is not recognised here; unrecognised terms are treated as non-total,
    which only ever *prevents* a rewrite, never enables an unsound one.
    """
    if memo is None:
        memo = {}
    if n.id in memo:
        return memo[n.id]
    op = n.op
    if op in ("const", "var"):
        r = True
    elif op == "undef":
        r = False
    elif op == "neg":
        r = is_total(n.args[0], memo)
    elif op in ("add", "sub", "mul"):
        r = is_total(n.args[0], memo) and is_total(n.args[1], memo)
    elif op == "div":
        a, b = n.args
        r = is_total(a, memo) and b.op == "const" and b.value != 0
    elif op == "cond":
        c, t, e = n.args
        if not is_total(c, memo):
            r = False
        elif c.op == "const":
            r = is_total(t if c.value != 0 else e, memo)
        else:
            r = is_total(t, memo) and is_total(e, memo)
    else:  # pragma: no cover - defensive
        r = False
    memo[n.id] = r
    return r


def why_total(n: Node) -> str:
    """Human-readable justification used inside rule certificates."""
    if n.op == "const":
        return "constant"
    if n.op == "var":
        return "variable (defined everywhere)"
    if n.op == "neg":
        return "negation of a total term"
    if n.op in ("add", "sub", "mul"):
        return "all subterms total"
    if n.op == "div":
        return "divisor is a nonzero constant"
    if n.op == "cond":
        return "condition total and required branch(es) total"
    return "not total"
