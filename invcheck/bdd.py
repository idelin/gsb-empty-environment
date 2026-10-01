"""Reduced ordered binary decision diagrams (ROBDD) with a shared unique table.

All Boolean functions handled by one manager share the same node pool, so
set operations (union/intersection/emptiness) are performed symbolically on
BDDs -- no per-state enumeration is involved.

Only the Python standard library is used.
"""

from __future__ import annotations


class ResourceLimitExceeded(Exception):
    """Raised when a configured resource budget (nodes or operations) is hit."""


class BDDManager:
    """A shared BDD manager with unique table and operation caches.

    Node ids 0 and 1 are the constant terminals False and True.
    """

    FALSE = 0
    TRUE = 1

    def __init__(self, variables, max_nodes=None, max_ops=None):
        self.variables = list(variables)
        if len(set(self.variables)) != len(self.variables):
            raise ValueError("duplicate variable names")
        self.level = {name: i for i, name in enumerate(self.variables)}
        # _nodes[node_id] = (var, low, high); terminals carry (None, id, id).
        self._nodes = [(None, 0, 0), (None, 1, 1)]
        self._unique = {}
        self._var_cache = {}
        self._ite_cache = {}
        self._neg_cache = {}
        self._quant_cache = {}
        self._rename_cache = {}
        self.max_nodes = max_nodes
        self.max_ops = max_ops
        self.op_count = 0

    # ------------------------------------------------------------------ utils

    def _tick(self, n=1):
        self.op_count += n
        if self.max_ops is not None and self.op_count > self.max_ops:
            raise ResourceLimitExceeded(
                f"operation budget %d exceeded" % self.max_ops
            )

    @property
    def node_count(self):
        return len(self._nodes)

    def reset_op_count(self):
        self.op_count = 0

    def mk(self, var, low, high):
        if var not in self.level:
            raise KeyError("unknown variable %r" % (var,))
        if low == high:
            return low
        key = (var, low, high)
        nid = self._unique.get(key)
        if nid is None:
            if self.max_nodes is not None and len(self._nodes) >= self.max_nodes:
                raise ResourceLimitExceeded(
                    "node budget %d exceeded" % self.max_nodes
                )
            nid = len(self._nodes)
            self._nodes.append(key)
            self._unique[key] = nid
        return nid

    def var(self, name):
        nid = self._var_cache.get(name)
        if nid is None:
            nid = self.mk(name, self.FALSE, self.TRUE)
            self._var_cache[name] = nid
        return nid

    def node(self, nid):
        return self._nodes[nid]

    def top_level(self, nid):
        return self.level[self._nodes[nid][0]]

    # ------------------------------------------------------------- negation

    def negate(self, f):
        if f < 2:
            return 1 - f
        r = self._neg_cache.get(f)
        if r is None:
            self._tick()
            var, low, high = self._nodes[f]
            r = self.mk(var, self.negate(low), self.negate(high))
            self._neg_cache[f] = r
        return r

    # ------------------------------------------------------------------ ITE

    def _cofactor(self, nid, var):
        """Return (high, low) cofactors of nid w.r.t. var.

        ``var`` must not be below the top variable of ``nid`` in the order.
        """
        if nid < 2:
            return nid, nid
        v, low, high = self._nodes[nid]
        if v == var:
            return high, low
        return nid, nid

    def _top_var3(self, f, g, h):
        best = None
        best_lvl = len(self.variables)
        for nid in (f, g, h):
            if nid >= 2:
                lvl = self.top_level(nid)
                if lvl < best_lvl:
                    best_lvl = lvl
                    best = self._nodes[nid][0]
        return best

    def ite(self, f, g, h):
        self._tick()
        if f == self.TRUE:
            return g
        if f == self.FALSE:
            return h
        if g == h:
            return g
        if g == self.TRUE and h == self.FALSE:
            return f
        if g == self.FALSE and h == self.TRUE:
            return self.negate(f)
        key = (f, g, h)
        r = self._ite_cache.get(key)
        if r is not None:
            return r
        v = self._top_var3(f, g, h)
        f1, f0 = self._cofactor(f, v)
        g1, g0 = self._cofactor(g, v)
        h1, h0 = self._cofactor(h, v)
        t = self.ite(f1, g1, h1)
        e = self.ite(f0, g0, h0)
        r = self.mk(v, e, t)
        self._ite_cache[key] = r
        return r

    # -------------------------------------------------------- boolean connectives

    def and_(self, f, g):
        return self.ite(f, g, self.FALSE)

    def or_(self, f, g):
        return self.ite(f, self.TRUE, g)

    def xor(self, f, g):
        return self.ite(f, self.negate(g), g)

    def implies(self, f, g):
        return self.ite(f, g, self.TRUE)

    def iff(self, f, g):
        return self.ite(f, g, self.negate(g))

    def not_(self, f):
        return self.negate(f)

    def and_all(self, fs):
        out = self.TRUE
        for f in fs:
            out = self.and_(out, f)
        return out

    def or_all(self, fs):
        out = self.FALSE
        for f in fs:
            out = self.or_(out, f)
        return out

    # ----------------------------------------------------------- quantification

    def exists(self, f, variables):
        return self._exists(f, frozenset(variables))

    def _exists(self, f, vset):
        if f < 2:
            return f
        key = (f, vset)
        r = self._quant_cache.get(key)
        if r is None:
            self._tick()
            var, low, high = self._nodes[f]
            lo = self._exists(low, vset)
            hi = self._exists(high, vset)
            if var in vset:
                r = self.or_(lo, hi)
            else:
                r = self.mk(var, lo, hi)
            self._quant_cache[key] = r
        return r

    def forall(self, f, variables):
        return self.negate(self.exists(self.negate(f), variables))

    # ---------------------------------------------------------------- renaming

    def rename(self, f, mapping):
        """Replace variables according to ``mapping`` (name -> name)."""
        mkey = tuple(sorted(mapping.items()))
        return self._rename(f, mapping, mkey)

    def _rename(self, f, mapping, mkey):
        if f < 2:
            return f
        key = (f, mkey)
        r = self._rename_cache.get(key)
        if r is None:
            self._tick()
            var, low, high = self._nodes[f]
            nv = mapping.get(var, var)
            lo = self._rename(low, mapping, mkey)
            hi = self._rename(high, mapping, mkey)
            r = self.ite(self.var(nv), hi, lo)
            self._rename_cache[key] = r
        return r

    # ---------------------------------------------------------------- queries

    def evaluate(self, f, assignment):
        """Evaluate f under a (possibly partial) truth assignment."""
        while f >= 2:
            var, low, high = self._nodes[f]
            f = high if assignment.get(var, False) else low
        return bool(f)

    def support(self, f):
        seen = set()
        stack = [f]
        out = set()
        while stack:
            n = stack.pop()
            if n < 2 or n in seen:
                continue
            seen.add(n)
            var, low, high = self._nodes[n]
            out.add(var)
            stack.append(low)
            stack.append(high)
        return out

    def sat_count(self, f):
        """Number of satisfying assignments over all manager variables."""
        n = len(self.variables)
        memo = {}

        def rec(node):
            r = memo.get(node)
            if r is None:
                var, low, high = self._nodes[node]
                lvl = self.level[var]
                r = _rec_at(low, lvl + 1) + _rec_at(high, lvl + 1)
                memo[node] = r
            return r

        def _rec_at(node, lvl):
            if node == 0:
                return 0
            if node == 1:
                return 1 << (n - lvl)
            return rec(node) << (self.level[self._nodes[node][0]] - lvl)

        return _rec_at(f, 0)

    # --------------------------------------------------------------- cubes

    def literal(self, name, phase):
        v = self.var(name)
        return v if phase else self.negate(v)

    def cube(self, assignment):
        """BDD for the conjunction of literals in a partial assignment."""
        out = self.TRUE
        for name in sorted(assignment, key=lambda v: self.level[v]):
            out = self.and_(out, self.literal(name, assignment[name]))
        return out

    def one_cube(self, f):
        """One satisfying partial assignment (a path to terminal 1)."""
        if f == self.FALSE:
            return None
        cube = {}
        while f >= 2:
            var, low, high = self._nodes[f]
            if low == self.FALSE:
                cube[var] = True
                f = high
            else:
                cube[var] = False
                f = low
        return cube

    def implies_true(self, f, g):
        """True iff f implies g, i.e. f & ~g is empty."""
        return self.and_(f, self.negate(g)) == self.FALSE

    def minimize_cube(self, f, cube):
        """Greedily drop literals while the cube still implies f.

        The result is irredundant: removing any remaining literal would
        admit an assignment outside f.
        """
        cube = dict(cube)
        for name in sorted(cube, key=lambda v: self.level[v]):
            if name not in cube:
                continue
            trial = {k: v for k, v in cube.items() if k != name}
            if self.implies_true(self.cube(trial), f):
                cube = trial
        return cube
