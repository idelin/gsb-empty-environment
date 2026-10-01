"""Discrete factors over finite-domain variables.

A factor maps every joint assignment of its variables to a non-negative
real number.  Tables are stored flat in row-major order with the *last*
variable varying fastest.
"""

from __future__ import annotations

from .errors import (
    DomainConflictError,
    InvalidEvidenceError,
    UnknownVariableError,
)


def _strides(sizes):
    """Row-major strides (last axis fastest) for the given axis sizes."""
    strides = [1] * len(sizes)
    acc = 1
    for i in range(len(sizes) - 1, -1, -1):
        strides[i] = acc
        acc *= sizes[i]
    return strides


def _decode(flat, sizes, strides):
    coords = []
    rem = flat
    for stride, size in zip(strides, sizes):
        coords.append(rem // stride)
        rem %= stride
    return coords


class Factor:
    """An immutable-by-convention factor over named discrete variables."""

    __slots__ = ("variables", "domains", "table")

    def __init__(self, variables, domains, table):
        variables = tuple(variables)
        missing = [v for v in variables if v not in domains]
        if missing:
            raise UnknownVariableError(
                f"no domain declared for variable(s): {missing}"
            )
        size = 1
        for v in variables:
            d = tuple(domains[v])
            if not d:
                raise ValueError(f"domain of {v!r} must be non-empty")
            size *= len(d)
        table = tuple(float(x) for x in table)
        if len(table) != size:
            raise ValueError(
                f"table has {len(table)} entries, expected {size} "
                f"for variables {variables}"
            )
        self.variables = variables
        self.domains = {v: tuple(domains[v]) for v in variables}
        self.table = table

    # ------------------------------------------------------------------
    @property
    def size(self):
        return len(self.table)

    def _sizes(self):
        return [len(self.domains[v]) for v in self.variables]

    def value(self, assignment):
        """Table value at a full assignment of this factor's variables."""
        strides = _strides(self._sizes())
        flat = 0
        for v, stride in zip(self.variables, strides):
            flat += self.domains[v].index(assignment[v]) * stride
        return self.table[flat]

    # ------------------------------------------------------------------
    def restrict(self, evidence):
        """Fix evidence variables, dropping them from the factor."""
        evidence = {k: v for k, v in evidence.items() if k in self.domains}
        if not evidence:
            return self
        for var, val in evidence.items():
            if val not in self.domains[var]:
                raise InvalidEvidenceError(
                    f"evidence {var}={val!r} not in domain {self.domains[var]}"
                )
        keep = tuple(v for v in self.variables if v not in evidence)
        if not keep:
            return Factor((), self.domains, [self.value(evidence)])
        keep_domains = {v: self.domains[v] for v in keep}
        keep_sizes = [len(keep_domains[v]) for v in keep]
        keep_strides = _strides(keep_sizes)
        out = [0.0] * (keep_strides[0] * keep_sizes[0])
        for flat in range(len(out)):
            coords = _decode(flat, keep_sizes, keep_strides)
            full = dict(zip(keep, coords))
            full.update({v: self.domains[v].index(val) for v, val in evidence.items()})
            src = 0
            for v, stride in zip(self.variables, _strides(self._sizes())):
                src += full[v] * stride
            out[flat] = self.table[src]
        return Factor(keep, keep_domains, out)

    # ------------------------------------------------------------------
    def multiply(self, other):
        """Pointwise product over the union of both scopes."""
        for v in self.variables:
            if v in other.domains and self.domains[v] != other.domains[v]:
                raise DomainConflictError(
                    f"variable {v!r} has domain {self.domains[v]} here but "
                    f"{other.domains[v]} in the other factor"
                )
        out_vars = self.variables + tuple(
            v for v in other.variables if v not in self.domains
        )
        out_domains = dict(self.domains)
        out_domains.update(
            {v: other.domains[v] for v in other.variables if v not in out_domains}
        )
        out_sizes = [len(out_domains[v]) for v in out_vars]
        out_strides = _strides(out_sizes)
        total = out_strides[0] * out_sizes[0] if out_sizes else 1

        self_pos = [out_vars.index(v) for v in self.variables]
        other_pos = [out_vars.index(v) for v in other.variables]
        self_strides = _strides(self._sizes())
        other_strides = _strides(other._sizes())

        out = [0.0] * total
        for flat in range(total):
            coords = _decode(flat, out_sizes, out_strides)
            si = sum(coords[p] * s for p, s in zip(self_pos, self_strides))
            oi = sum(coords[p] * s for p, s in zip(other_pos, other_strides))
            out[flat] = self.table[si] * other.table[oi]
        return Factor(out_vars, out_domains, out)

    # ------------------------------------------------------------------
    def sum_out(self, var):
        """Marginalize `var` out by summation."""
        if var not in self.variables:
            raise UnknownVariableError(f"{var!r} is not in this factor")
        keep = tuple(v for v in self.variables if v != var)
        keep_domains = {v: self.domains[v] for v in keep}
        if not keep:
            return Factor((), self.domains, [sum(self.table)])
        keep_sizes = [len(keep_domains[v]) for v in keep]
        keep_strides = _strides(keep_sizes)
        total = keep_strides[0] * keep_sizes[0]
        out = [0.0] * total
        axis = self.variables.index(var)
        self_sizes = self._sizes()
        self_strides = _strides(self_sizes)
        var_stride = self_strides[axis]
        for flat in range(total):
            coords = _decode(flat, keep_sizes, keep_strides)
            coord_of = dict(zip(keep, coords))
            src = 0
            for i, v in enumerate(self.variables):
                if v != var:
                    src += coord_of[v] * self_strides[i]
            acc = 0.0
            for k in range(self_sizes[axis]):
                acc += self.table[src + k * var_stride]
            out[flat] = acc
        return Factor(keep, keep_domains, out)

    # ------------------------------------------------------------------
    def normalize(self):
        """Return (normalized_factor, Z) with Z = sum of the table."""
        z = sum(self.table)
        if z == 0.0:
            return Factor(self.variables, self.domains, self.table), 0.0
        return (
            Factor(self.variables, self.domains, [x / z for x in self.table]),
            z,
        )

    def probabilities(self):
        """Assignment tuple -> table value, keyed by domain values."""
        sizes = self._sizes()
        strides = _strides(sizes)
        out = {}
        for flat in range(len(self.table)):
            coords = _decode(flat, sizes, strides)
            key = tuple(
                self.domains[v][c] for v, c in zip(self.variables, coords)
            )
            out[key] = self.table[flat]
        return out

    def __repr__(self):
        return (
            f"Factor(variables={self.variables!r}, size={len(self.table)})"
        )
