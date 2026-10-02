"""Shared reduced ordered binary decision diagrams (ROBDD).

A single :class:`BDDManager` owns one unique table, so every BDD built
through it is *shared*: structurally equal functions are the same node
and set operations (and/or/not/quantification/restriction/renaming)
are performed symbolically on the diagrams, never by enumerating the
underlying state assignments.

Node identifiers ``0`` and ``1`` are the False/True terminals.
"""

from __future__ import annotations

import time

FALSE = 0
TRUE = 1

_OPS = {
    "and": lambda a, b: a and b,
    "or": lambda a, b: a or b,
    "xor": lambda a, b: a != b,
    "implies": lambda a, b: (not a) or b,
    "iff": lambda a, b: a == b,
    "diff": lambda a, b: a and not b,
}
_COMMUTATIVE = {"and", "or", "xor", "iff"}


class ResourceExceeded(Exception):
    """Raised when a configured node or time budget is exhausted."""


class BDDManager:
    """ROBDD manager with a shared unique table and optional budgets."""

    def __init__(self, variables, node_limit=None, time_limit=None):
        self.variables = list(variables)
        self.level = {v: i for i, v in enumerate(self.variables)}
        self._nodes = [None, None]  # id 0 -> False, id 1 -> True
        self._unique = {}
        self._apply_cache = {}
        self._neg_cache = {}
        self._exists_cache = {}
        self._restrict_cache = {}
        self._rename_cache = {}
        self.node_limit = node_limit
        self._deadline = None if time_limit is None else time.monotonic() + time_limit

    # ------------------------------------------------------------------
    # budget
    # ------------------------------------------------------------------
    def _check_budget(self):
        if self.node_limit is not None and len(self._nodes) > self.node_limit:
            raise ResourceExceeded(f"node limit {self.node_limit} exceeded")
        if self._deadline is not None and time.monotonic() > self._deadline:
            raise ResourceExceeded("time limit exceeded")

    @property
    def num_nodes(self):
        return len(self._nodes)

    # ------------------------------------------------------------------
    # construction
    # ------------------------------------------------------------------
    def mk(self, var, lo, hi):
        if lo == hi:
            return lo
        key = (var, lo, hi)
        nid = self._unique.get(key)
        if nid is None:
            self._check_budget()
            nid = len(self._nodes)
            self._unique[key] = nid
            self._nodes.append(key)
        return nid

    def var(self, name):
        if name not in self.level:
            raise KeyError(f"unknown variable {name!r}")
        return self.mk(name, FALSE, TRUE)

    def constant(self, value):
        return TRUE if value else FALSE

    # ------------------------------------------------------------------
    # internals
    # ------------------------------------------------------------------
    def node(self, nid):
        """Return ``(var, lo, hi)`` for a decision node, None for terminals."""
        return self._nodes[nid]

    def _top(self, f, g):
        vf = self._nodes[f][0] if f >= 2 else None
        vg = self._nodes[g][0] if g >= 2 else None
        if vf is None:
            return vg
        if vg is None:
            return vf
        return vf if self.level[vf] <= self.level[vg] else vg

    def _cofactor(self, f, var):
        if f >= 2 and self._nodes[f][0] == var:
            return self._nodes[f][1], self._nodes[f][2]
        return f, f

    # ------------------------------------------------------------------
    # boolean operations
    # ------------------------------------------------------------------
    def apply(self, op, f, g):
        if op not in _OPS:
            raise ValueError(f"unknown op {op!r}")
        if op in _COMMUTATIVE and f > g:
            f, g = g, f
        if op == "and":
            if f == FALSE or g == FALSE:
                return FALSE
            if f == TRUE:
                return g
            if g == TRUE:
                return f
            if f == g:
                return f
        elif op == "or":
            if f == TRUE or g == TRUE:
                return TRUE
            if f == FALSE:
                return g
            if g == FALSE:
                return f
            if f == g:
                return f
        elif op == "xor":
            if f == FALSE:
                return g
            if g == FALSE:
                return f
            if f == g:
                return FALSE
        elif op == "iff":
            if f == TRUE:
                return g
            if g == TRUE:
                return f
            if f == g:
                return TRUE
        elif op == "implies":
            if f == FALSE or g == TRUE:
                return TRUE
            if f == TRUE:
                return g
            if g == FALSE:
                return self.negate(f)
            if f == g:
                return TRUE
        elif op == "diff":
            if f == FALSE or g == TRUE:
                return FALSE
            if g == FALSE:
                return f
            if f == g:
                return FALSE
        if f < 2 and g < 2:
            return TRUE if _OPS[op](bool(f), bool(g)) else FALSE
        key = (op, f, g)
        hit = self._apply_cache.get(key)
        if hit is not None:
            return hit
        var = self._top(f, g)
        flo, fhi = self._cofactor(f, var)
        glo, ghi = self._cofactor(g, var)
        lo = self.apply(op, flo, glo)
        hi = self.apply(op, fhi, ghi)
        res = self.mk(var, lo, hi)
        self._apply_cache[key] = res
        return res

    def negate(self, f):
        if f < 2:
            return TRUE - f
        hit = self._neg_cache.get(f)
        if hit is None:
            var, lo, hi = self._nodes[f]
            hit = self.mk(var, self.negate(lo), self.negate(hi))
            self._neg_cache[f] = hit
        return hit

    # ------------------------------------------------------------------
    # quantification / substitution
    # ------------------------------------------------------------------
    def exists(self, f, qvars):
        """Existentially quantify ``qvars`` out of ``f``."""
        return self._exists(f, frozenset(qvars))

    def _exists(self, f, qvars):
        if f < 2:
            return f
        key = (f, qvars)
        hit = self._exists_cache.get(key)
        if hit is not None:
            return hit
        var, lo, hi = self._nodes[f]
        rlo = self._exists(lo, qvars)
        rhi = self._exists(hi, qvars)
        if var in qvars:
            res = self.apply("or", rlo, rhi)
        else:
            res = self.mk(var, rlo, rhi)
        self._exists_cache[key] = res
        return res

    def restrict(self, f, assignment):
        """Substitute concrete boolean values for the given variables."""
        assignment = {k: bool(v) for k, v in assignment.items()}
        if not assignment:
            return f
        return self._restrict(f, tuple(sorted(assignment.items())), assignment)

    def _restrict(self, f, key, assignment):
        if f < 2:
            return f
        ck = (f, key)
        hit = self._restrict_cache.get(ck)
        if hit is not None:
            return hit
        var, lo, hi = self._nodes[f]
        if var in assignment:
            res = self._restrict(hi if assignment[var] else lo, key, assignment)
        else:
            res = self.mk(
                var,
                self._restrict(lo, key, assignment),
                self._restrict(hi, key, assignment),
            )
        self._restrict_cache[ck] = res
        return res

    def rename(self, f, mapping):
        """Rename variables; the mapping must preserve the variable order."""
        mapping = {k: v for k, v in mapping.items() if k != v}
        if not mapping:
            return f
        sup = self.support(f)
        ordered = sorted(sup, key=lambda v: self.level[v])
        images = [mapping.get(v, v) for v in ordered]
        if len(set(images)) != len(images):
            raise ValueError("rename mapping is not injective on the support")
        if images != sorted(images, key=lambda v: self.level[v]):
            raise ValueError("rename mapping does not preserve variable order")
        return self._rename(f, tuple(sorted(mapping.items())), mapping)

    def _rename(self, f, key, mapping):
        if f < 2:
            return f
        ck = (f, key)
        hit = self._rename_cache.get(ck)
        if hit is not None:
            return hit
        var, lo, hi = self._nodes[f]
        res = self.mk(
            mapping.get(var, var),
            self._rename(lo, key, mapping),
            self._rename(hi, key, mapping),
        )
        self._rename_cache[ck] = res
        return res

    # ------------------------------------------------------------------
    # inspection
    # ------------------------------------------------------------------
    def support(self, f):
        seen = set()
        out = set()
        stack = [f]
        while stack:
            n = stack.pop()
            if n < 2 or n in seen:
                continue
            seen.add(n)
            var, lo, hi = self._nodes[n]
            out.add(var)
            stack.append(lo)
            stack.append(hi)
        return out

    def eval(self, f, assignment):
        while f >= 2:
            var, lo, hi = self._nodes[f]
            f = hi if assignment.get(var, False) else lo
        return bool(f)

    def pick_sat(self, f):
        """One satisfying assignment, lexicographically minimal (False < True)
        under the manager's variable order; None if ``f`` is unsatisfiable."""
        if f == FALSE:
            return None
        assign = {}
        node = f
        while node >= 2:
            var, lo, hi = self._nodes[node]
            if lo != FALSE:
                assign[var] = False
                node = lo
            else:
                assign[var] = True
                node = hi
        for v in self.variables:
            assign.setdefault(v, False)
        return assign

    def sat_count(self, f, variables=None):
        """Number of satisfying assignments over ``variables`` (default: all)."""
        variables = list(self.variables if variables is None else variables)
        variables.sort(key=self.level.__getitem__)
        lvl_of = {v: i for i, v in enumerate(variables)}
        memo = {}

        def rec(node, idx):
            if node == FALSE:
                return 0
            if node == TRUE:
                return 1 << (len(variables) - idx)
            key = (node, idx)
            if key in memo:
                return memo[key]
            var, lo, hi = self._nodes[node]
            j = lvl_of[var]
            res = (rec(lo, j + 1) + rec(hi, j + 1)) << (j - idx)
            memo[key] = res
            return res

        return rec(f, 0)
