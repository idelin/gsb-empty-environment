"""Cardinality-constraint encodings: sequential counter and network based.

Both encodings are projectively equivalent to the cardinality constraint:
an assignment to the original variables satisfies the constraint iff it can
be extended to the auxiliary variables so that the CNF is satisfied.

Boundary simplification (decided purely by semantics, no encoding built):
  at-most k : k < 0  -> trivially false | k >= n -> trivially true
              k == 0 -> unit clauses ~x_i
  at-least k: k <= 0 -> trivially true | k > n  -> trivially false
              k == n -> unit clauses x_i
  exactly k : k < 0 or k > n -> trivially false (n = 0, k = 0 is trivially
              true, i.e. the empty CNF)
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .core import CNF

ATMOST, ATLEAST, EXACTLY = "atmost", "atleast", "exactly"


def check_direct(op, k, values):
    """Independent checker: evaluate the cardinality constraint directly."""
    total = sum(1 for v in values if v)
    if op == ATMOST:
        return total <= k
    if op == ATLEAST:
        return total >= k
    if op == EXACTLY:
        return total == k
    raise ValueError(f"unknown operator: {op!r}")


@dataclass
class Encoding:
    """Result of encoding one cardinality constraint."""

    op: str
    k: int
    lits: list
    cnf: CNF
    aux: list = field(default_factory=list)
    status: str = "encoded"  # encoded | simplified | trivial-true | trivial-false
    method: str = "?"
    _lift_fn: object = None

    @property
    def num_clauses(self):
        return self.cnf.num_clauses

    @property
    def num_aux(self):
        return len(self.aux)

    def lift(self, values):
        """Extend a satisfying original assignment to the auxiliary variables.

        ``values`` maps original variable ids to booleans.  The returned dict
        assigns every auxiliary variable so that the CNF is satisfied
        (projection-equivalence witness, direction "constraint => CNF").
        """
        if self._lift_fn is None:
            return {}
        return self._lift_fn(values)


def _trivial(op, k, lits, status, method):
    enc = Encoding(op, k, list(lits), CNF(), [], status, method)
    if status == "trivial-false":
        enc.cnf.add_clause([])  # the empty clause: unsatisfiable
    return enc


def _units(op, k, lits, sign, method):
    cnf = CNF()
    for lit in lits:
        cnf.add(lit if sign else -lit)
    return Encoding(op, k, list(lits), cnf, [], "simplified", method)


# ---------------------------------------------------------------------------
# Sequential counter (Sinz, 2005)
# ---------------------------------------------------------------------------

def _sinz_at_most(pool, lits, k, tag):
    """Sinz sequential counter for sum(lits) <= k; requires 0 < k < len(lits)."""
    n = len(lits)
    cnf = CNF()
    s = {}
    for i in range(1, n + 1):
        for j in range(1, k + 1):
            s[i, j] = pool.new_var(f"{tag}.s{i}.{j}")
    cnf.add(-lits[0], s[1, 1])
    for i in range(2, n + 1):
        xi = lits[i - 1]
        cnf.add(-xi, s[i, 1])
        cnf.add(-s[i - 1, 1], s[i, 1])
        cnf.add(-xi, -s[i - 1, k])
        for j in range(2, k + 1):
            cnf.add(-s[i - 1, j], s[i, j])
            cnf.add(-xi, -s[i - 1, j - 1], s[i, j])

    def lift(values):
        def litval(lit):
            v = values[abs(lit)]
            return v if lit > 0 else not v

        out = {}
        run = 0
        for i in range(1, n + 1):
            if litval(lits[i - 1]):
                run += 1
            for j in range(1, k + 1):
                out[s[i, j]] = run >= j
        return out

    return cnf, list(s.values()), lift


def _seq_at_most(pool, lits, k, tag):
    n = len(lits)
    if k < 0:
        return _trivial(ATMOST, k, lits, "trivial-false", "seq")
    if k >= n:
        return _trivial(ATMOST, k, lits, "trivial-true", "seq")
    if k == 0:
        return _units(ATMOST, k, lits, False, "seq")
    cnf, aux, lift = _sinz_at_most(pool, lits, k, tag)
    return Encoding(ATMOST, k, list(lits), cnf, aux, "encoded", "seq", lift)


def _seq_at_least(pool, lits, k, tag):
    n = len(lits)
    if k <= 0:
        return _trivial(ATLEAST, k, lits, "trivial-true", "seq")
    if k > n:
        return _trivial(ATLEAST, k, lits, "trivial-false", "seq")
    # sum(lits) >= k  <=>  sum(negated lits) <= n - k
    inner = _seq_at_most(pool, [-lit for lit in lits], n - k, tag + ".neg")
    return Encoding(ATLEAST, k, list(lits), inner.cnf, inner.aux,
                    inner.status, "seq", inner._lift_fn)


def _seq_exactly(pool, lits, k, tag):
    n = len(lits)
    if k < 0 or k > n:
        return _trivial(EXACTLY, k, lits, "trivial-false", "seq")
    upper = _seq_at_most(pool, lits, k, tag + ".am")
    lower = _seq_at_least(pool, lits, k, tag + ".al")
    cnf = CNF()
    cnf.extend(upper.cnf)
    cnf.extend(lower.cnf)
    if upper.status == lower.status == "trivial-true":
        status = "trivial-true"  # covers n == 0, k == 0
    elif upper.status == "encoded" or lower.status == "encoded":
        status = "encoded"
    else:
        status = "simplified"

    def lift(values):
        out = upper.lift(values)
        out.update(lower.lift(values))
        return out

    return Encoding(EXACTLY, k, list(lits), cnf, upper.aux + lower.aux,
                    status, "seq", lift)


# ---------------------------------------------------------------------------
# Network-based encoding (odd-even mergesort sorting network)
# ---------------------------------------------------------------------------

def _oddeven_comparators(m):
    """Comparators of Batcher's odd-even mergesort network for m inputs."""
    comps = []

    def merge(lo, n, r):
        step = r * 2
        if step < n:
            merge(lo, n, step)
            merge(lo + r, n, step)
            for i in range(lo + r, lo + n - r, step):
                comps.append((i, i + r))
        else:
            comps.append((lo, lo + r))

    def sort(lo, n):
        if n > 1:
            half = n // 2
            sort(lo, half)
            sort(lo + half, half)
            merge(lo, n, 1)

    if m > 1:
        sort(0, m)
    return comps


class _SortingNetwork:
    """Sorts input literals descending; outputs[t] <=> at least t+1 inputs true."""

    def __init__(self, pool, lits, tag):
        n = len(lits)
        m = 1
        while m < n:
            m *= 2
        self.cnf = CNF()
        self.aux = []
        self.zero = None
        inputs = list(lits)
        if m > n:  # pad with a constant-false variable
            self.zero = pool.new_var(f"{tag}.zero")
            self.cnf.add(-self.zero)
            self.aux.append(self.zero)
            inputs.extend([self.zero] * (m - n))
        self._input_wires = inputs
        self._comps = []
        wires = list(inputs)
        for (i, j) in _oddeven_comparators(m):
            a, b = wires[i], wires[j]
            hi = pool.new_var(f"{tag}.hi{len(self.aux)}")
            lo = pool.new_var(f"{tag}.lo{len(self.aux) + 1}")
            # hi <-> a OR b
            self.cnf.add(-a, hi)
            self.cnf.add(-b, hi)
            self.cnf.add(a, b, -hi)
            # lo <-> a AND b
            self.cnf.add(-lo, a)
            self.cnf.add(-lo, b)
            self.cnf.add(-a, -b, lo)
            self.aux.extend([hi, lo])
            self._comps.append((i, j, hi, lo))
            wires[i] = hi  # descending order: larger value first
            wires[j] = lo
        self.outputs = wires

    def lift(self, values):
        out = {}
        if self.zero is not None:
            out[self.zero] = False

        def litval(lit):
            if lit in out:
                return out[lit]
            v = values[abs(lit)]
            return v if lit > 0 else not v

        pos_vals = [litval(lit) for lit in self._input_wires]
        for (i, j, hi, lo) in self._comps:
            a, b = pos_vals[i], pos_vals[j]
            hi_v = a or b
            lo_v = a and b
            out[hi] = hi_v
            out[lo] = lo_v
            pos_vals[i] = hi_v
            pos_vals[j] = lo_v
        return out


def _net_at_most(pool, lits, k, tag):
    n = len(lits)
    if k < 0:
        return _trivial(ATMOST, k, lits, "trivial-false", "net")
    if k >= n:
        return _trivial(ATMOST, k, lits, "trivial-true", "net")
    if k == 0:
        return _units(ATMOST, k, lits, False, "net")
    sorter = _SortingNetwork(pool, lits, tag)
    sorter.cnf.add(-sorter.outputs[k])  # the (k+1)-th largest must be false
    return Encoding(ATMOST, k, list(lits), sorter.cnf, sorter.aux,
                    "encoded", "net", sorter.lift)


def _net_at_least(pool, lits, k, tag):
    n = len(lits)
    if k <= 0:
        return _trivial(ATLEAST, k, lits, "trivial-true", "net")
    if k > n:
        return _trivial(ATLEAST, k, lits, "trivial-false", "net")
    if k == n:
        return _units(ATLEAST, k, lits, True, "net")
    sorter = _SortingNetwork(pool, lits, tag)
    sorter.cnf.add(sorter.outputs[k - 1])  # the k-th largest must be true
    return Encoding(ATLEAST, k, list(lits), sorter.cnf, sorter.aux,
                    "encoded", "net", sorter.lift)


def _net_exactly(pool, lits, k, tag):
    n = len(lits)
    if k < 0 or k > n:
        return _trivial(EXACTLY, k, lits, "trivial-false", "net")
    if n == 0:  # k == 0: empty CNF, satisfiable
        return _trivial(EXACTLY, k, lits, "trivial-true", "net")
    if k == 0:
        enc = _net_at_most(pool, lits, 0, tag)
        enc.op = EXACTLY
        return enc
    if k == n:
        enc = _net_at_least(pool, lits, n, tag)
        enc.op = EXACTLY
        return enc
    sorter = _SortingNetwork(pool, lits, tag)  # one shared network
    sorter.cnf.add(sorter.outputs[k - 1])
    sorter.cnf.add(-sorter.outputs[k])
    return Encoding(EXACTLY, k, list(lits), sorter.cnf, sorter.aux,
                    "encoded", "net", sorter.lift)


# ---------------------------------------------------------------------------
# Dispatcher
# ---------------------------------------------------------------------------

_BUILDERS = {
    "seq": {ATMOST: _seq_at_most, ATLEAST: _seq_at_least, EXACTLY: _seq_exactly},
    "net": {ATMOST: _net_at_most, ATLEAST: _net_at_least, EXACTLY: _net_exactly},
}


def encode(pool, lits, op, k, method="seq", tag=None):
    """Encode the constraint ``op(lits, k)`` with the given method.

    ``method`` is "seq" (sequential counter) or "net" (sorting network).
    Auxiliary variables are drawn from ``pool`` and never clash with the
    original variables or with auxiliaries of other encodings on the pool.
    """
    if method not in _BUILDERS:
        raise ValueError(f"unknown method: {method!r}")
    if op not in _BUILDERS[method]:
        raise ValueError(f"unknown operator: {op!r}")
    if tag is None:
        tag = pool.fresh_tag(f"{method}.{op}{k}")
    return _BUILDERS[method][op](pool, list(lits), k, tag)
