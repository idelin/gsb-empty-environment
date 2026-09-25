"""Exact checker for small chains: enumerate the input-box vertices.

The chain deviation ``x -> f(x) - f_hat(x)`` is linear in ``x``, so
``|f(x) - f_hat(x)|_inf`` is convex and its maximum over the input box is
attained at a vertex.  Enumerating all ``2**n`` vertices therefore gives
the *true* maximum deviation -- a ground truth the a-priori certificate
must cover.  The observed error is used only to check the certificate,
never as a substitute for the bound itself.
"""
from __future__ import annotations

from dataclasses import dataclass
from itertools import product

from .dense import from_entries, linf_norm, matvec


@dataclass
class VerificationReport:
    observed_linf: float          # true max deviation over all vertices
    certified_linf: float         # a-priori certificate bound
    ok: bool                      # certificate covers the true deviation
    argmax_vertex: tuple | None   # vertex where the max deviation occurs
    num_vertices: int


def evaluate(shapes, entries_per_layer, x) -> list[float]:
    """Dense self-written evaluation of a chain at point ``x``."""
    z = list(x)
    for (nrows, ncols), entries in zip(shapes, entries_per_layer):
        z = matvec(from_entries(nrows, ncols, entries), z)
    return z


def vertices(box):
    """Yield every vertex of the input box."""
    corners = [(iv.lo, iv.hi) for iv in box]
    for choice in product(*corners):
        yield tuple(choice)


def max_deviation(shapes, exact_entries, approx_entries, box):
    """True max L-inf deviation over the box (exact, via vertex enumeration)."""
    best = 0.0
    arg = None
    count = 0
    for v in vertices(box):
        count += 1
        d = linf_norm([a - b for a, b in zip(
            evaluate(shapes, exact_entries, v),
            evaluate(shapes, approx_entries, v))])
        if d > best:
            best, arg = d, v
    return best, arg, count


def check_certificate(truncator, tol: float = 1e-9) -> VerificationReport:
    """Verify a ChainTruncator's certificate against ground truth."""
    cert = truncator.certificate()
    shapes = truncator.shapes
    exact = [truncator.original_entries(i) for i in range(len(shapes))]
    approx = [truncator.kept_entries(i) for i in range(len(shapes))]
    observed, arg, count = max_deviation(shapes, exact, approx,
                                         truncator.input_box)
    return VerificationReport(
        observed_linf=observed,
        certified_linf=cert.linf,
        ok=observed <= cert.linf + tol,
        argmax_vertex=arg,
        num_vertices=count,
    )
