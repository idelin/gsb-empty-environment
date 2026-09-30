"""Independent direct evaluator, used only to cross-check solution sets.

It evaluates the *original* expression at a rational point with plain
Fraction arithmetic -- no Sturm theory, no cell decomposition, no code shared
with the solver's decision procedure.  Sampling is a sanity check, never a
completeness proof; irrational boundary points are covered by the certificate
verifier instead.
"""
from __future__ import annotations

from fractions import Fraction

from . import polys as P
from .expr import Atom, iter_atoms, op_truth


def evaluate(expr, x):
    """Direct domain-aware evaluation of ``expr`` at rational ``x``."""
    x = Fraction(x)
    for a in iter_atoms(expr):
        if P.evaluate(a.den, x) == 0:
            return False  # outside the common domain
    return _eval(expr, x)


def _eval(e, x):
    if isinstance(e, Atom):
        v = P.evaluate(e.num, x) / P.evaluate(e.den, x)
        return op_truth(e.op, (v > 0) - (v < 0))
    if e.kind == "and":
        return all(_eval(c, x) for c in e.children)
    if e.kind == "or":
        return any(_eval(c, x) for c in e.children)
    if e.kind == "not":
        return not _eval(e.children[0], x)
    raise TypeError(e)


def cross_check(expr, realset, samples, budget=None):
    """Return the sample points where set membership and direct evaluation disagree."""
    bad = []
    for s in samples:
        s = Fraction(s)
        if evaluate(expr, s) != realset.contains_point(s, budget):
            bad.append(s)
    return bad
