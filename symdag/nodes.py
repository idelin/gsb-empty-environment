"""Hash-consed DAG nodes for symbolic rational expressions.

Nodes are immutable and interned by a Factory: structurally identical
subexpressions are the same object (a DAG, not a tree).  Every node
carries an ``origin`` path: the position (tuple of child indices) of its
first occurrence in the originally parsed expression.  Simplification
preserves origins, so an error raised while interpreting a simplified
expression can be mapped back to the source position in the original
expression.  No host eval/exec is used anywhere.
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

OPS_ARITY = {
    "add": 2, "sub": 2, "mul": 2, "div": 2,
    "neg": 1, "if": 3, "lt": 2, "eq": 2,
}
COMMUTATIVE = {"add", "mul"}
LEAF_OPS = {"const", "var", "undef"}


@dataclass(frozen=True, eq=False)
class Node:
    """An immutable DAG node.  Identity-hashed; interned by Factory."""

    op: str                      # 'const' | 'var' | 'undef' | op tag
    args: tuple = ()             # child Nodes
    value: Fraction | None = None   # for 'const'
    name: str | None = None         # for 'var'
    origin: tuple = ()           # path of first occurrence in the source expr
    reason: str = ""             # for 'undef': why it is undefined
    key: tuple = ()              # canonical structural key (origin-free)


class Factory:
    """Creates and interns nodes; owns totality / free-variable caches."""

    def __init__(self) -> None:
        self._intern: dict[tuple, Node] = {}
        self._fv_memo: dict[Node, frozenset] = {}
        self._tot_memo: dict[Node, bool] = {}

    # -- raw constructor -------------------------------------------------
    def _make(self, op, args=(), value=None, name=None, origin=(), reason=""):
        args = tuple(args)
        key = (op, tuple(a.key for a in args), value, name, reason)
        node = self._intern.get(key)
        if node is None:
            node = Node(op=op, args=args, value=value, name=name,
                        origin=tuple(origin), reason=reason, key=key)
            self._intern[key] = node
        return node

    # -- leaves ------------------------------------------------------------
    def const(self, value, origin=()):
        return self._make("const", value=Fraction(value), origin=origin)

    def var(self, name, origin=()):
        return self._make("var", name=name, origin=origin)

    def undef(self, origin=(), reason="undefined"):
        return self._make("undef", origin=origin, reason=reason)

    # -- operators ---------------------------------------------------------
    def _op(self, op, args, origin):
        args = list(args)
        if op in COMMUTATIVE and repr(args[1].key) < repr(args[0].key):
            args.reverse()  # canonical operand order for + and *
        return self._make(op, args=args, origin=origin)

    def add(self, a, b, origin=()):
        return self._op("add", (a, b), origin)

    def sub(self, a, b, origin=()):
        return self._op("sub", (a, b), origin)

    def mul(self, a, b, origin=()):
        return self._op("mul", (a, b), origin)

    def div(self, a, b, origin=()):
        return self._op("div", (a, b), origin)

    def neg(self, a, origin=()):
        return self._op("neg", (a,), origin)

    def if_(self, c, t, e, origin=()):
        return self._op("if", (c, t, e), origin)

    def lt(self, a, b, origin=()):
        return self._op("lt", (a, b), origin)

    def eq(self, a, b, origin=()):
        return self._op("eq", (a, b), origin)

    # -- parsing a tree spec into the DAG ----------------------------------
    def parse(self, spec, origin=()):
        """Build a DAG from a nested tuple spec, labelling origin paths.

        spec := ('const', q) | ('var', name) | (op, child, ...)
        with op in add/sub/mul/div/neg/if/lt/eq.
        """
        tag = spec[0]
        if tag == "const":
            return self.const(spec[1], origin)
        if tag == "var":
            return self.var(spec[1], origin)
        if tag not in OPS_ARITY:
            raise ValueError(f"unknown op {tag!r}")
        kids = [self.parse(s, origin + (i,)) for i, s in enumerate(spec[1:])]
        ctor = {
            "add": self.add, "sub": self.sub, "mul": self.mul,
            "div": self.div, "neg": self.neg, "if": self.if_,
            "lt": self.lt, "eq": self.eq,
        }[tag]
        return ctor(*kids, origin=origin)

    # -- analyses ------------------------------------------------------------
    def free_vars(self, node):
        """Variables occurring free in (the possibly simplified) node."""
        memo = self._fv_memo
        res = memo.get(node)
        if res is None:
            if node.op == "var":
                res = frozenset({node.name})
            elif node.op in ("const", "undef"):
                res = frozenset()
            else:
                res = frozenset().union(*(self.free_vars(a) for a in node.args))
            memo[node] = res
        return res

    def is_total(self, node):
        """Conservative check: node is defined for every rational assignment.

        Variables, constants and +,-,*,neg,lt,eq of total nodes are total.
        a/b is total only when b is a nonzero constant.  'undef' and any
        expression that may hit a zero divisor are NOT total.  Totality is
        binding-independent, which is what makes it safe to use in rewrite
        preconditions inside the incremental engine.
        """
        memo = self._tot_memo
        res = memo.get(node)
        if res is None:
            op = node.op
            if op == "const" or op == "var":
                res = True
            elif op == "undef":
                res = False
            elif op == "div":
                res = (self.is_total(node.args[0])
                       and self.is_total(node.args[1])
                       and node.args[1].op == "const"
                       and node.args[1].value != 0)
            else:
                res = all(self.is_total(a) for a in node.args)
            memo[node] = res
        return res


def to_str(node) -> str:
    """Canonical printable form of a (possibly shared) node."""
    op = node.op
    if op == "const":
        v = node.value
        return str(v.numerator) if v.denominator == 1 else f"{v.numerator}/{v.denominator}"
    if op == "var":
        return node.name
    if op == "undef":
        path = ".".join(str(i) for i in node.origin) or "root"
        return f"undefined@{path}({node.reason})"
    if op == "if":
        return f"(if {to_str(node.args[0])} {to_str(node.args[1])} {to_str(node.args[2])})"
    if op == "neg":
        return f"(neg {to_str(node.args[0])})"
    sym = {"add": "+", "sub": "-", "mul": "*", "div": "/", "lt": "<", "eq": "="}[op]
    return f"({sym} {to_str(node.args[0])} {to_str(node.args[1])})"
