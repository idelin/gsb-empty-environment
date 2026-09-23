"""Independent naive reference implementation used only for verification.

Polynomials are dicts {exponent: Fraction} with zero entries removed -- a
deliberately different representation and code path from ``polyq.core`` so
that the checks below are genuinely independent.  All arithmetic is exact
over Q.  No symbolic algebra library is used anywhere.
"""
from __future__ import annotations

from fractions import Fraction


def n_poly(coeffs) -> dict:
    """Dense iterable (index = power) -> sparse dict."""
    return {i: Fraction(c) for i, c in enumerate(coeffs) if Fraction(c) != 0}


def n_deg(p: dict) -> int:
    return max(p) if p else -1


def n_lc(p: dict) -> Fraction:
    if not p:
        raise ValueError("zero polynomial has no leading coefficient")
    return p[n_deg(p)]


def n_add(a: dict, b: dict) -> dict:
    r = dict(a)
    for e, c in b.items():
        r[e] = r.get(e, Fraction(0)) + c
        if r[e] == 0:
            del r[e]
    return r


def n_neg(a: dict) -> dict:
    return {e: -c for e, c in a.items()}


def n_sub(a: dict, b: dict) -> dict:
    return n_add(a, n_neg(b))


def n_mul(a: dict, b: dict) -> dict:
    r: dict = {}
    for ea, ca in a.items():
        for eb, cb in b.items():
            e = ea + eb
            r[e] = r.get(e, Fraction(0)) + ca * cb
            if r[e] == 0:
                del r[e]
    return r


def n_scale(a: dict, c) -> dict:
    c = Fraction(c)
    if c == 0:
        return {}
    return {e: c * v for e, v in a.items()}


def n_monic(a: dict) -> dict:
    if not a:
        return {}
    return n_scale(a, Fraction(1, 1) / n_lc(a))


def n_divmod(a: dict, b: dict):
    """Naive long division over Q[x]: a == q*b + r, deg r < deg b.

    Raises ZeroDivisionError on the zero divisor.  Valid over any field.
    """
    if not b:
        raise ZeroDivisionError("polynomial division by the zero polynomial")
    q: dict = {}
    r = dict(a)
    db = n_deg(b)
    lb = n_lc(b)
    while r and n_deg(r) >= db:
        e = n_deg(r) - db
        c = n_lc(r) / lb
        q[e] = q.get(e, Fraction(0)) + c
        r = n_sub(r, n_mul({e: c}, b))
    return q, r


def n_pseudo_divmod(a: dict, b: dict):
    """Naive pseudo-division: returns (q, r, factor) with factor*a == q*b + r.

    factor is lc(b)**k for the number k of rounds actually performed.
    Valid over any integral domain.
    """
    if not b:
        raise ZeroDivisionError("polynomial pseudo-division by the zero polynomial")
    if n_deg(a) < n_deg(b):
        return {}, dict(a), Fraction(1)
    d = n_lc(b)
    q: dict = {}
    r = dict(a)
    factor = Fraction(1)
    while r and n_deg(r) >= n_deg(b):
        e = n_deg(r) - n_deg(b)
        s = n_lc(r)
        q = n_add(n_scale(q, d), {e: s})
        r = n_sub(n_scale(r, d), n_mul({e: s}, b))
        factor *= d
    return q, r, factor


def n_extended_gcd(a: dict, b: dict):
    """Naive extended Euclid over Q[x]; returns monic (g, s, t)."""
    if not a and not b:
        return {}, {}, {}
    if not a:
        return n_monic(b), {}, n_scale({0: Fraction(1)}, Fraction(1, 1) / n_lc(b))
    if not b:
        return n_monic(a), n_scale({0: Fraction(1)}, Fraction(1, 1) / n_lc(a)), {}
    r0, r1 = a, b
    s0, s1 = {0: Fraction(1)}, {}
    t0, t1 = {}, {0: Fraction(1)}
    while r1:
        q, r = n_divmod(r0, r1)
        r0, r1 = r1, r
        s0, s1 = s1, n_sub(s0, n_mul(q, s1))
        t0, t1 = t1, n_sub(t0, n_mul(q, t1))
    inv = Fraction(1, 1) / n_lc(r0)
    return n_scale(r0, inv), n_scale(s0, inv), n_scale(t0, inv)


# ---------------------------------------------------------------------------
# Independent checkers (raise AssertionError on any violation)
# ---------------------------------------------------------------------------

def check_division(a, b, q, r) -> None:
    """Verify a == q*b + r and deg r < deg b, all recomputed naively."""
    A, B, Q, R = map(_as_dict, (a, b, q, r))
    assert B, "divisor must be nonzero"
    assert n_sub(A, n_add(n_mul(Q, B), R)) == {}, "division identity a = q*b + r failed"
    assert n_deg(R) < n_deg(B), "remainder degree not below divisor degree"


def check_bezout(a, b, g, s, t) -> None:
    """Verify s*a + t*b == g exactly (naive recomputation)."""
    A, B, G, S, T = map(_as_dict, (a, b, g, s, t))
    assert n_add(n_mul(S, A), n_mul(T, B)) == G, "Bezout identity s*a + t*b = g failed"


def check_gcd(a, b, g, s, t) -> None:
    """Certify that g is THE monic gcd of a, b.

    The certificate is the exact Bezout identity s*a + t*b == g: any common
    divisor of a and b must divide g.  Combined with g | a and g | b
    (checked by naive division) and monic normalisation, g is the unique
    monic greatest common divisor.  We never infer maximality from
    divisibility alone.
    """
    A, B, G = _as_dict(a), _as_dict(b), _as_dict(g)
    if not A and not B:
        assert not G, "gcd(0, 0) must be the zero polynomial"
        return
    check_bezout(a, b, g, s, t)                      # common divisors divide g
    assert n_divmod(A, G)[1] == {}, "g does not divide a"
    assert n_divmod(B, G)[1] == {}, "g does not divide b"
    assert n_lc(G) == 1, "gcd is not monic"


def check_prs_steps(A0, B0, steps) -> None:
    """Independently verify every PRS transition and its Bezout certificate."""
    A, B = _as_dict(A0), _as_dict(B0)
    prev_deg = None
    for st in steps:
        dv, dr = _as_dict(st.dividend), _as_dict(st.divisor)
        q, prem = _as_dict(st.pseudo_quotient), _as_dict(st.pseudo_remainder)
        rem = _as_dict(st.remainder)
        delta = n_deg(dv) - n_deg(dr)
        assert delta >= 0, "PRS degrees must be non-increasing"
        # 1. pseudo-division, recomputed naively from scratch
        q_ref, prem_ref, factor = n_pseudo_divmod(dv, dr)
        assert q == q_ref and prem == prem_ref, \
            f"step {st.index}: pseudo-division mismatch with naive reference"
        assert n_sub(n_scale(dv, factor), n_add(n_mul(q, dr), prem)) == {}, \
            f"step {st.index}: pseudo-division identity failed"
        # 2. subresultant scaling: remainder == prem / beta
        assert n_sub(n_scale(rem, st.beta), prem) == {}, \
            f"step {st.index}: remainder != pseudo_remainder / beta"
        # 3. degree descent
        assert n_deg(rem) < n_deg(dr), f"step {st.index}: no degree descent"
        if prev_deg is not None:
            assert n_deg(rem) < prev_deg, f"step {st.index}: PRS not descending"
        prev_deg = n_deg(rem)
        # 4. per-step Bezout certificate against the ORIGINAL inputs
        s, t = _as_dict(st.bezout_s), _as_dict(st.bezout_t)
        assert n_add(n_mul(s, A), n_mul(t, B)) == rem, \
            f"step {st.index}: Bezout certificate failed"


def _as_dict(p) -> dict:
    """Accept either representation (dense list or sparse dict)."""
    if isinstance(p, dict):
        return {e: Fraction(c) for e, c in p.items() if Fraction(c) != 0}
    return n_poly(p)
