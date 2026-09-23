"""Independent verifier: recomputes everything from scratch and rechecks
certificates, disjointness, and completeness of an isolation result."""

from __future__ import annotations

from typing import List

from .engine import IsolationResult, RootCertificate
from .polynomials import (ONE, degree, evaluate, is_zero, mul, poly,
                          squarefree_factorization)
from .sturm import cauchy_bound, sign_variations, sturm_sequence


def verify_certificate(p, cert: RootCertificate) -> List[str]:
    """Return a list of problems; an empty list means the cert is valid."""
    problems: List[str] = []
    p = poly(p)
    if is_zero(p):
        return ["zero polynomial admitted"]
    factors = squarefree_factorization(p)
    factor_mult = {f: m for f, m in factors}
    if cert.squarefree_factor not in factor_mult:
        problems.append("square-free factor not part of the factorization")
    elif factor_mult[cert.squarefree_factor] != cert.multiplicity:
        problems.append("multiplicity does not match the factor index")
    q = ONE
    for f, _ in factors:
        q = mul(q, f)
    seq = sturm_sequence(q)
    if cert.root_count != 1:
        problems.append("certificate must claim exactly one root")
    if cert.kind == "point":
        if cert.point is None:
            problems.append("point certificate without a point")
            return problems
        if evaluate(q, cert.point) != 0:
            problems.append("point is not an exact root")
        for f, _m in factors:
            vanishes = evaluate(f, cert.point) == 0
            if f == cert.squarefree_factor and not vanishes:
                problems.append("claimed factor does not vanish at the point")
            if f != cert.squarefree_factor and vanishes:
                problems.append("another factor also vanishes at the point")
    elif cert.kind == "interval":
        iv = cert.interval
        if iv is None:
            problems.append("interval certificate without an interval")
            return problems
        if not iv.lo < iv.hi:
            problems.append("degenerate interval")
        vlo, slo = sign_variations(seq, iv.lo)
        vhi, shi = sign_variations(seq, iv.hi)
        if (vlo, slo) != (cert.variations_lo, cert.signs_lo):
            problems.append("endpoint data at lo does not reproduce")
        if (vhi, shi) != (cert.variations_hi, cert.signs_hi):
            problems.append("endpoint data at hi does not reproduce")
        count = vlo - vhi - (1 if iv.hi_excluded else 0)
        if count != 1:
            problems.append("interval does not isolate exactly one root")
        if iv.hi_excluded and evaluate(q, iv.hi) != 0:
            problems.append("hi excluded but not an exact root")
        if not iv.hi_excluded and evaluate(q, iv.hi) == 0:
            problems.append(
                "hi is an exact root but not excluded: inconsistent counting")
        owners = 0
        for f, m in factors:
            fseq = sturm_sequence(f)
            wlo, _ = sign_variations(fseq, iv.lo)
            whi, _ = sign_variations(fseq, iv.hi)
            cnt = wlo - whi
            if iv.hi_excluded and evaluate(f, iv.hi) == 0:
                cnt -= 1
            if cnt == 1:
                owners += 1
                if f != cert.squarefree_factor:
                    problems.append("interval root owned by a different factor")
                elif m != cert.multiplicity:
                    problems.append(
                        "owning factor gives a different multiplicity")
            elif cnt != 0:
                problems.append("factor count inside interval is inconsistent")
        if owners != 1:
            problems.append("interval root not owned by exactly one factor")
    else:
        problems.append(f"unknown certificate kind {cert.kind!r}")
    return problems


def verify_isolation(result: IsolationResult) -> List[str]:
    """Recheck a whole isolation: certificates, disjointness, completeness."""
    problems: List[str] = []
    p = result.polynomial
    if is_zero(p):
        return ["zero polynomial admitted"]
    factors = squarefree_factorization(p)
    if tuple(factors) != tuple(result.squarefree_factors):
        problems.append("square-free factorization does not reproduce")
    q = ONE
    for f, _ in factors:
        q = mul(q, f)
    if q != result.squarefree_part:
        problems.append("square-free part does not reproduce")
    for cert in result.roots:
        problems.extend(verify_certificate(p, cert))
    intervals = sorted(
        (c.interval for c in result.roots if c.kind == "interval"),
        key=lambda iv: iv.lo)
    for a, b in zip(intervals, intervals[1:]):
        if a.hi > b.lo:
            problems.append("isolating intervals overlap")
    points = [c.point for c in result.roots if c.kind == "point"]
    if len(set(points)) != len(points):
        problems.append("duplicate point roots")
    for pt in points:
        for iv in intervals:
            inside = iv.lo < pt < iv.hi or (pt == iv.hi and not iv.hi_excluded)
            if inside:
                problems.append("point root lies inside an isolating interval")
    seq = sturm_sequence(q)
    accounted = len(result.roots)
    for pend in result.pending:
        iv = pend.interval
        vlo, _ = sign_variations(seq, iv.lo)
        vhi, _ = sign_variations(seq, iv.hi)
        if (vlo, vhi) != (pend.variations_lo, pend.variations_hi):
            problems.append("pending endpoint data does not reproduce")
        cnt = vlo - vhi - (1 if iv.hi_excluded else 0)
        if cnt != pend.root_count:
            problems.append("pending root count does not reproduce")
        accounted += pend.root_count
    if degree(q) > 0:
        bound = cauchy_bound(q)
        vlo, _ = sign_variations(seq, -bound)
        vhi, _ = sign_variations(seq, bound)
        total = vlo - vhi
    else:
        total = 0
    if result.total_distinct_roots != total:
        problems.append("declared total does not match the Sturm count")
    if accounted != total:
        problems.append("isolated roots plus pending counts miss some roots")
    if result.completed != (not result.pending):
        problems.append("completion flag inconsistent with pending intervals")
    return problems
