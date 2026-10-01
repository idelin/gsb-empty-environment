"""Variable elimination for finite discrete factor graphs.

Pure standard library, Python 3.11+.

Features
--------
- Exact marginals via variable elimination over flat float tables.
- Elimination order either supplied by the caller or chosen greedily to
  keep intermediate factor tables small (min-size heuristic).
- Evidence constraints; zero probabilities are fully supported.
- The normalization constant Z (= P(evidence)) is reported; contradictory
  evidence yields Z == 0 and raises :class:`ContradictoryEvidenceError`.
- Variables shared across factors must declare identical domains, otherwise
  :class:`DomainMismatchError` is raised.
- Empirical estimates via Gibbs sampling with a caller-injected
  ``random.Random``; the seed and sample count are recorded on the result.
- Exact and empirical results are distinct types, both labelled ``kind``.
"""

from __future__ import annotations

from dataclasses import dataclass
from functools import reduce
from itertools import product
from math import fsum, prod
import random


# ---------------------------------------------------------------------------
# Errors
# ---------------------------------------------------------------------------

class FactorGraphError(Exception):
    """Base class for all errors raised by this module."""


class DomainMismatchError(FactorGraphError):
    """The same variable was declared with two different domains."""


class ContradictoryEvidenceError(FactorGraphError):
    """The evidence has probability zero (normalization constant is 0)."""


class UnknownVariableError(FactorGraphError):
    """A query, evidence or elimination variable is not in the graph."""


# ---------------------------------------------------------------------------
# Factor
# ---------------------------------------------------------------------------

class Factor:
    """A non-negative real table over a finite set of discrete variables.

    ``table`` is a flat list in row-major order over ``variables`` (the last
    variable varies fastest).  Zero entries are allowed and meaningful.
    """

    __slots__ = ("variables", "domains", "table", "_positions")

    def __init__(self, variables, domains, table):
        self.variables = tuple(variables)
        if len(set(self.variables)) != len(self.variables):
            raise ValueError(f"duplicate variables in {self.variables!r}")
        self.domains = {}
        for var in self.variables:
            domain = tuple(domains[var])
            if not domain:
                raise ValueError(f"empty domain for variable {var!r}")
            self.domains[var] = domain
        self.table = [float(x) for x in table]
        expected = prod(len(self.domains[v]) for v in self.variables)
        if len(self.table) != expected:
            raise ValueError(
                f"table has {len(self.table)} entries, expected {expected}")
        if any(x < 0 for x in self.table):
            raise ValueError("factor tables must be non-negative")
        self._positions = {
            v: {val: i for i, val in enumerate(self.domains[v])}
            for v in self.variables
        }

    def value(self, assignment):
        """Table entry for a (possibly larger) assignment dict."""
        index = 0
        for var in self.variables:
            index = index * len(self.domains[var]) + \
                self._positions[var][assignment[var]]
        return self.table[index]

    def restrict(self, evidence):
        """Return the factor with evidence variables fixed to their values."""
        relevant = {v: evidence[v] for v in self.variables if v in evidence}
        if not relevant:
            return self
        for var, val in relevant.items():
            if val not in self.domains[var]:
                raise ContradictoryEvidenceError(
                    f"evidence {var}={val!r} is outside domain "
                    f"{self.domains[var]!r}")
        keep = tuple(v for v in self.variables if v not in relevant)
        keep_domains = {v: self.domains[v] for v in keep}
        table = []
        for values in product(*(keep_domains[v] for v in keep)):
            assignment = dict(zip(keep, values))
            assignment.update(relevant)
            table.append(self.value(assignment))
        return Factor(keep, keep_domains, table)

    def reorder(self, variables):
        """Return an equivalent factor with variables in the given order."""
        variables = tuple(variables)
        if set(variables) != set(self.variables):
            raise ValueError("reorder must permute the existing variables")
        domains = {v: self.domains[v] for v in variables}
        table = [self.value(dict(zip(variables, values)))
                 for values in product(*(domains[v] for v in variables))]
        return Factor(variables, domains, table)

    def __repr__(self):
        return f"Factor(variables={self.variables!r}, table={self.table!r})"


def multiply(left, right):
    """Pointwise product of two factors over the union of their scopes."""
    shared = set(left.variables) & set(right.variables)
    for var in shared:
        if left.domains[var] != right.domains[var]:
            raise DomainMismatchError(
                f"variable {var!r} has domains {left.domains[var]!r} and "
                f"{right.domains[var]!r}")
    variables = left.variables + tuple(
        v for v in right.variables if v not in shared)
    domains = {v: left.domains.get(v, right.domains.get(v))
               for v in variables}
    table = [left.value(a) * right.value(a)
             for a in (dict(zip(variables, values))
                       for values in product(*(domains[v] for v in variables)))]
    return Factor(variables, domains, table)


def sum_out(factor, var):
    """Sum ``var`` out of ``factor``."""
    if var not in factor.variables:
        raise UnknownVariableError(
            f"variable {var!r} not in factor over {factor.variables!r}")
    keep = tuple(v for v in factor.variables if v != var)
    domains = {v: factor.domains[v] for v in keep}
    table = []
    for values in product(*(domains[v] for v in keep)):
        assignment = dict(zip(keep, values))
        total = 0.0
        for val in factor.domains[var]:
            assignment[var] = val
            total += factor.value(assignment)
        table.append(total)
    return Factor(keep, domains, table)


# ---------------------------------------------------------------------------
# Marginal result types (exact vs empirical are explicitly labelled)
# ---------------------------------------------------------------------------

class Marginal:
    """Read-only view over a marginal probability table."""

    kind = "?"  # overridden by subclasses: "exact" or "empirical"
    variables: tuple
    domains: dict
    probabilities: tuple

    def probability(self, key):
        """Look up one entry.

        ``key`` is a plain value for single-variable marginals or an
        assignment dict ``{var: value}`` for joint marginals.
        """
        if not isinstance(key, dict):
            if len(self.variables) != 1:
                raise TypeError(
                    "joint marginal requires an assignment dict key")
            key = {self.variables[0]: key}
        index = 0
        for var in self.variables:
            domain = self.domains[var]
            index = index * len(domain) + domain.index(key[var])
        return self.probabilities[index]

    def as_dict(self):
        """Return ``{value_or_tuple: probability}`` for the whole table."""
        out = {}
        for values in product(*(self.domains[v] for v in self.variables)):
            assignment = dict(zip(self.variables, values))
            out_key = values[0] if len(self.variables) == 1 else values
            out_key = out_key if self.variables else ()
            out[out_key] = self.probability(assignment) if self.variables \
                else self.probabilities[0]
        return out

    def __repr__(self):
        return (f"{type(self).__name__}(kind={self.kind!r}, "
                f"variables={self.variables!r}, table={self.as_dict()!r})")


@dataclass(frozen=True)
class ExactMarginal(Marginal):
    """Exact marginal plus the normalization constant Z = P(evidence)."""

    variables: tuple
    domains: dict
    probabilities: tuple
    normalization: float
    kind = "exact"


@dataclass(frozen=True)
class EmpiricalMarginal(Marginal):
    """Sampling-based estimate; records the seed and the sample count."""

    variables: tuple
    domains: dict
    probabilities: tuple
    samples: int
    seed: object
    burn_in: int
    kind = "empirical"


# ---------------------------------------------------------------------------
# Factor graph
# ---------------------------------------------------------------------------

class FactorGraph:
    """A collection of factors over a shared set of discrete variables."""

    def __init__(self):
        self.factors = []
        self.domains = {}
        self._factors_with = {}

    # -- construction ------------------------------------------------------

    def add_factor(self, factor):
        """Add a factor, enforcing domain consistency across the graph."""
        for var, domain in factor.domains.items():
            if var in self.domains:
                if self.domains[var] != domain:
                    raise DomainMismatchError(
                        f"variable {var!r} already has domain "
                        f"{self.domains[var]!r}, got {domain!r}")
            else:
                self.domains[var] = domain
        self.factors.append(factor)
        for var in factor.variables:
            self._factors_with.setdefault(var, []).append(factor)
        return factor

    def add(self, variables, domains, table):
        """Convenience wrapper: build and add a :class:`Factor`."""
        return self.add_factor(Factor(variables, domains, table))

    # -- validation --------------------------------------------------------

    def _validate_query(self, query):
        for var in query:
            if var not in self.domains:
                raise UnknownVariableError(f"unknown query variable {var!r}")
        if len(set(query)) != len(query):
            raise ValueError(f"duplicate query variables in {query!r}")

    def _validate_evidence(self, evidence):
        for var, val in evidence.items():
            if var not in self.domains:
                raise UnknownVariableError(
                    f"unknown evidence variable {var!r}")
            if val not in self.domains[var]:
                raise ContradictoryEvidenceError(
                    f"evidence {var}={val!r} is outside domain "
                    f"{self.domains[var]!r}")

    def _prepare(self, query, evidence):
        query = tuple(query)
        evidence = dict(evidence or {})
        self._validate_query(query)
        self._validate_evidence(evidence)
        overlap = set(query) & set(evidence)
        if overlap:
            raise ValueError(
                f"variables cannot be both queried and observed: {overlap!r}")
        hidden = [v for v in self.domains
                  if v not in query and v not in evidence]
        return query, evidence, hidden

    # -- exact inference ---------------------------------------------------

    def eliminate(self, query, evidence=None, order=None):
        """Exact marginal of ``query`` given ``evidence`` (variable elimination).

        ``order`` lists the hidden variables in elimination order; when
        omitted a greedy min-size heuristic keeps intermediate tables small.
        Returns an :class:`ExactMarginal` whose ``normalization`` field is
        Z = P(evidence).  Raises :class:`ContradictoryEvidenceError` when
        Z == 0.
        """
        query, evidence, hidden = self._prepare(query, evidence)
        factors = [f.restrict(evidence) for f in self.factors]
        if order is None:
            order = greedy_order(
                [set(f.variables) for f in factors], hidden, self.domains)
        else:
            order = list(order)
            if sorted(order) != sorted(hidden):
                raise ValueError(
                    f"order {order!r} is not a permutation of the hidden "
                    f"variables {hidden!r}")
        for var in order:
            involved = [f for f in factors if var in f.variables]
            if not involved:
                continue
            rest = [f for f in factors if var not in f.variables]
            combined = reduce(multiply, involved)
            factors = rest + [sum_out(combined, var)]
        result = reduce(multiply, factors, Factor((), {}, [1.0]))
        if query:
            result = result.reorder(query)
        z = fsum(result.table)
        if z == 0.0:
            raise ContradictoryEvidenceError(
                f"evidence {evidence!r} has zero probability (Z = 0)")
        return ExactMarginal(
            variables=result.variables,
            domains=result.domains,
            probabilities=tuple(x / z for x in result.table),
            normalization=z,
        )

    def enumerate_marginal(self, query, evidence=None):
        """Exact marginal by brute-force enumeration (ground truth, small graphs)."""
        query, evidence, hidden = self._prepare(query, evidence)
        table = []
        for qvalues in product(*(self.domains[v] for v in query)) if query \
                else [()]:
            total = 0.0
            for hvalues in product(*(self.domains[v] for v in hidden)):
                assignment = dict(evidence)
                assignment.update(zip(query, qvalues))
                assignment.update(zip(hidden, hvalues))
                weight = 1.0
                for factor in self.factors:
                    weight *= factor.value(assignment)
                    if weight == 0.0:
                        break
                total += weight
            table.append(total)
        z = fsum(table)
        if z == 0.0:
            raise ContradictoryEvidenceError(
                f"evidence {evidence!r} has zero probability (Z = 0)")
        return ExactMarginal(
            variables=query,
            domains={v: self.domains[v] for v in query},
            probabilities=tuple(x / z for x in table),
            normalization=z,
        )

    # -- empirical inference -------------------------------------------------

    def sample_gibbs(self, query, evidence=None, *, rng, samples,
                     burn_in=200, seed=None):
        """Empirical marginal of ``query`` via Gibbs sampling.

        The random source is injected by the caller as ``rng`` (a
        ``random.Random`` instance).  ``seed`` is recorded on the result for
        provenance; the caller is expected to have created
        ``rng = random.Random(seed)``.  Returns an :class:`EmpiricalMarginal`
        labelled ``kind="empirical"`` with ``samples`` and ``seed`` recorded.
        """
        if not isinstance(rng, random.Random):
            raise TypeError(
                "caller must inject the random source as random.Random")
        if samples <= 0:
            raise ValueError("samples must be positive")
        query, evidence, _ = self._prepare(query, evidence)
        # Reject contradictory evidence up front: Z == 0 must be an error,
        # never a silently meaningless sample stream.
        self.eliminate((), evidence)
        state = {v: domain[0] for v, domain in self.domains.items()}
        state.update(evidence)
        free = [v for v in self.domains if v not in evidence]
        counts = {}
        for step in range(burn_in + samples):
            for var in free:
                weights = []
                for val in self.domains[var]:
                    state[var] = val
                    weight = 1.0
                    for factor in self._factors_with[var]:
                        weight *= factor.value(state)
                        if weight == 0.0:
                            break
                    weights.append(weight)
                if fsum(weights) == 0.0:
                    raise ContradictoryEvidenceError(
                        f"evidence {evidence!r} leaves no valid state for "
                        f"variable {var!r} (Z = 0)")
                state[var] = rng.choices(self.domains[var],
                                         weights=weights)[0]
            if step >= burn_in:
                key = tuple(state[v] for v in query)
                counts[key] = counts.get(key, 0) + 1
        probabilities = tuple(
            counts.get(qvalues, 0) / samples
            for qvalues in (product(*(self.domains[v] for v in query))
                            if query else [()]))
        return EmpiricalMarginal(
            variables=query,
            domains={v: self.domains[v] for v in query},
            probabilities=probabilities,
            samples=samples,
            seed=seed,
            burn_in=burn_in,
        )


# ---------------------------------------------------------------------------
# Elimination ordering helpers
# ---------------------------------------------------------------------------

def greedy_order(scopes, hidden, domains):
    """Greedy min-size elimination order for the hidden variables.

    Repeatedly eliminates the variable whose resulting intermediate factor
    (product of the domain sizes of all variables joined through it) is
    smallest.
    """
    scopes = [set(s) for s in scopes]
    remaining = list(hidden)
    order = []
    while remaining:
        def cost(var):
            scope = set()
            for s in scopes:
                if var in s:
                    scope |= s
            scope.discard(var)
            return prod(len(domains[v]) for v in scope), scope

        best = min(remaining, key=lambda v: cost(v)[0])
        _, scope = cost(best)
        order.append(best)
        scopes = [s for s in scopes if best not in s]
        if scope:
            scopes.append(scope)
        remaining.remove(best)
    return order


def elimination_width(scopes, order, domains):
    """Largest intermediate factor table size produced by ``order``."""
    scopes = [set(s) for s in scopes]
    worst = 0
    for var in order:
        scope = set()
        for s in scopes:
            if var in s:
                scope |= s
        size = prod(len(domains[v]) for v in scope)
        worst = max(worst, size)
        scope.discard(var)
        scopes = [s for s in scopes if var not in s]
        if scope:
            scopes.append(scope)
    return worst
