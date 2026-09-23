"""Hash-consed expression DAG.

Every subexpression is interned exactly once, so a shared subexpression is
a single node and is therefore charged exactly once in the operation count.
Cost = number of reachable add/mul nodes (leaves -- constants and
variables -- are free).
"""
from __future__ import annotations

from fractions import Fraction

from .poly import Poly


class Dag:
    def __init__(self):
        self._nodes = []   # id -> (op, args)
        self._intern = {}  # (op, args) -> id

    def _node(self, op, args):
        key = (op, args)
        nid = self._intern.get(key)
        if nid is None:
            nid = len(self._nodes)
            self._nodes.append(key)
            self._intern[key] = nid
        return nid

    def const(self, value):
        return self._node("const", (Fraction(value),))

    def var(self, name):
        return self._node("var", (name,))

    def op(self, nid):
        return self._nodes[nid][0]

    def args(self, nid):
        return self._nodes[nid][1]

    def add(self, a, b):
        args = []
        for nid in (a, b):
            if self.op(nid) == "add":
                args.extend(self.args(nid))
            else:
                args.append(nid)
        total = Fraction(0)
        rest = []
        for nid in args:
            if self.op(nid) == "const":
                total += self.args(nid)[0]
            else:
                rest.append(nid)
        if total:
            rest.append(self.const(total))
        if not rest:
            return self.const(Fraction(0))
        if len(rest) == 1:
            return rest[0]
        return self._node("add", tuple(sorted(rest)))

    def mul(self, a, b):
        args = []
        for nid in (a, b):
            if self.op(nid) == "mul":
                args.extend(self.args(nid))
            else:
                args.append(nid)
        coeff = Fraction(1)
        rest = []
        for nid in args:
            if self.op(nid) == "const":
                coeff *= self.args(nid)[0]
            else:
                rest.append(nid)
        if coeff == 0:
            return self.const(Fraction(0))
        if coeff != 1 or not rest:
            rest.insert(0, self.const(coeff))
        if len(rest) == 1:
            return rest[0]
        return self._node("mul", tuple(sorted(rest)))

    def term_node(self, coeff, mono, variables):
        """Node for coeff * x^mono (a single flat mul node at most)."""
        node = None
        if coeff != 1 or not any(mono):
            node = self.const(coeff)
        for name, exp in zip(variables, mono):
            for _ in range(exp):
                v = self.var(name)
                node = v if node is None else self.mul(node, v)
        return node

    # -- cost: shared subexpressions are counted once ----------------------
    def cost(self, root):
        seen = set()
        stack = [root]
        total = 0
        while stack:
            nid = stack.pop()
            if nid in seen:
                continue
            seen.add(nid)
            if self.op(nid) in ("add", "mul"):
                total += 1
                stack.extend(self.args(nid))
        return total

    # -- independent canonical expansion (the verification path) ----------
    def expand(self, root, variables):
        memo = {}

        def go(nid):
            if nid in memo:
                return memo[nid]
            op = self.op(nid)
            if op == "const":
                result = Poly.constant(variables, self.args(nid)[0])
            elif op == "var":
                result = Poly.var(variables, self.args(nid)[0])
            elif op == "add":
                result = Poly.zero(variables)
                for child in self.args(nid):
                    result = result + go(child)
            else:
                result = Poly.constant(variables, 1)
                for child in self.args(nid):
                    result = result * go(child)
            memo[nid] = result
            return result

        return go(root)

    # -- printing ----------------------------------------------------------
    def to_string(self, root):
        return self._fmt(root, 0)

    def _prec(self, nid):
        return {"const": 3, "var": 3, "mul": 2, "add": 1}[self.op(nid)]

    def _fmt(self, nid, parent_prec):
        op = self.op(nid)
        if op == "const":
            return str(self.args(nid)[0])
        if op == "var":
            return self.args(nid)[0]
        if op == "add":
            text = self._fmt_add(nid)
        else:
            text = self._fmt_mul(nid)
        if self._prec(nid) < parent_prec:
            return f"({text})"
        return text

    def _fmt_add(self, nid):
        parts = []
        for child in self.args(nid):
            sign, body = self._signed_body(child)
            if not parts:
                parts.append(("-" if sign < 0 else "") + body)
            else:
                parts.append((" - " if sign < 0 else " + ") + body)
        return "".join(parts)

    def _signed_body(self, nid):
        """Return (sign, printed body) for a child of an add node."""
        if self.op(nid) == "const":
            value = self.args(nid)[0]
            return (1 if value >= 0 else -1), str(abs(value))
        if self.op(nid) == "mul":
            sign, factors = self._mul_factors(nid)
            return sign, "*".join(factors)
        return 1, self._fmt(nid, 1)

    def _fmt_mul(self, nid):
        sign, factors = self._mul_factors(nid)
        return ("-" if sign < 0 else "") + "*".join(factors)

    def _mul_factors(self, nid):
        args = list(self.args(nid))
        coeff = None
        for i, child in enumerate(args):
            if self.op(child) == "const":
                coeff = self.args(child)[0]
                args.pop(i)
                break
        sign = 1
        if coeff is not None and coeff < 0:
            sign = -1
            coeff = -coeff
        factors = []
        if coeff is not None and not (coeff == 1 and args):
            factors.append(str(coeff))
        i = 0
        while i < len(args):
            child = args[i]
            if self.op(child) == "var":
                name = self.args(child)[0]
                count = 0
                while i < len(args) and args[i] == child:
                    count += 1
                    i += 1
                factors.append(name if count == 1 else f"{name}^{count}")
            else:
                factors.append(self._fmt(child, 2))
                i += 1
        if not factors:
            factors.append("1")
        return sign, factors
