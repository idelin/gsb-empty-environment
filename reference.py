"""Independent naive reference implementations, used ONLY for validation.

These are deliberately simple schoolbook algorithms sharing no code with
polygcd.py / prs.py, so that checks are genuinely independent. GCD
correctness is established by exact identities (Bezout), degree descent,
monic normalization, and agreement with this independent computation --
never by a divisibility-only argument.
"""
from __future__ import annotations

from fractions import Fraction


def n_trim(p):
    p = [Fraction(c) for c in p]
    while p and p[-1] == 0:
        p.pop()
    return p


def n_add(a, b):
    a = n_trim(a)
    b = n_trim(b)
    n = max(len(a), len(b))
    out = []
    for i in range(n):
        x = a[i] if i < len(a) else Fraction(0)
        y = b[i] if i < len(b) else Fraction(0)
        out.append(x + y)
    return n_trim(out)


def n_mul(a, b):
    a = n_trim(a)
    b = n_trim(b)
    if not a or not b:
        return []
    out = [Fraction(0)] * (len(a) + len(b) - 1)
    for i in range(len(a)):
        for j in range(len(b)):
            out[i + j] += a[i] * b[j]
    return n_trim(out)


def n_scale(a, c):
    return n_trim([Fraction(c) * x for x in a])


def naive_divmod(f, g):
    """Schoolbook long division over Q. Returns (q, r), deg r < deg g."""
    f = n_trim(f)
    g = n_trim(g)
    if not g:
        raise ZeroDivisionError("naive_divmod: zero divisor polynomial")
    r = f[:]
    q = [Fraction(0)] * max(0, len(f) - len(g) + 1)
    while r and len(r) >= len(g):
        k = len(r) - len(g)
        c = r[-1] / g[-1]
        q[k] = c
        new_r = []
        for i in range(len(r)):
            coeff = r[i]
            j = i - k
            if 0 <= j < len(g):
                coeff = coeff - c * g[j]
            new_r.append(coeff)
        r = n_trim(new_r)
    return n_trim(q), r


def naive_gcd(f, g):
    """Plain Euclidean algorithm built on naive_divmod; monic result."""
    f = n_trim(f)
    g = n_trim(g)
    if not f and not g:
        return []
    while g:
        _, r = naive_divmod(f, g)
        f, g = g, r
    lead = f[-1]
    return [c / lead for c in f]


def check_division(f, g, q, r):
    """Verify f == q*g + r exactly and deg r < deg g. Raises AssertionError."""
    f = n_trim(f)
    g = n_trim(g)
    q = n_trim(q)
    r = n_trim(r)
    if not g:
        raise AssertionError("divisor is the zero polynomial")
    if n_add(n_mul(q, g), r) != f:
        raise AssertionError("division identity f = q*g + r failed")
    if r and len(r) >= len(g):
        raise AssertionError("degree descent violated: deg r >= deg g")
    return True


def validate_gcd(f, g, d, s, t):
    """Validate a gcd result WITHOUT a divisibility-only proof.

    All checks are exact:
      1. Bezout identity s*f + t*g == d holds (d is in the ideal (f, g)).
      2. d is monic, or d == 0 iff f == g == 0 (normalization).
      3. d agrees with an independently computed naive Euclidean gcd,
         including its degree (maximality, via degree descent of the
         independent Euclidean remainder sequence).
    """
    f = n_trim(f)
    g = n_trim(g)
    d = n_trim(d)
    s = n_trim(s)
    t = n_trim(t)
    if n_add(n_mul(s, f), n_mul(t, g)) != d:
        raise AssertionError("Bezout identity s*f + t*g == d failed")
    if not f and not g:
        if d:
            raise AssertionError("gcd(0, 0) must be 0")
        return True
    if not d or d[-1] != 1:
        raise AssertionError("gcd is not monic (normalization failed)")
    ref = naive_gcd(f, g)
    if ref != d:
        raise AssertionError("result disagrees with independent naive gcd")
    if len(ref) != len(d):
        raise AssertionError("degree mismatch with independent naive gcd")
    return True


def validate_prs_steps(F, G, steps, d, s, t):
    """Validate every PRS step transformation plus the final gcd.

    Per prs-step, all exact:
      - pseudo-division identity: multiplier*A == Q*B + R;
      - growth-control division: R == beta * next_remainder;
      - Bezout certificate: s_i*F + t_i*G == next_remainder;
      - strict degree descent along the remainder sequence.
    Then the final (d, s, t) is checked by validate_gcd.
    """
    F = n_trim(F)
    G = n_trim(G)
    prev_deg = None
    for st in steps:
        if st.get("kind") != "prs-step":
            continue
        A = n_trim(st["dividend"])
        B = n_trim(st["divisor"])
        q = n_trim(st["pseudo_quotient"])
        r = n_trim(st["pseudo_remainder"])
        mult = Fraction(st["multiplier"])
        beta = Fraction(st["beta"])
        nxt = n_trim(st["remainder"])
        if n_add(n_mul(q, B), r) != n_scale(A, mult):
            raise AssertionError("pseudo-division identity failed")
        if n_scale(nxt, beta) != r:
            raise AssertionError("remainder != pseudo_remainder / beta")
        s_i = n_trim(st["bezout_s"])
        t_i = n_trim(st["bezout_t"])
        if n_add(n_mul(s_i, F), n_mul(t_i, G)) != nxt:
            raise AssertionError("step Bezout certificate failed")
        dg = len(nxt) - 1 if nxt else -1
        if prev_deg is not None and dg >= prev_deg:
            raise AssertionError("PRS degrees not strictly decreasing")
        prev_deg = dg
    return validate_gcd(F, G, d, s, t)
