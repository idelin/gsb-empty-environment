"""Certificates of the solving derivation, and an independent verifier.

The certificate records every reviewable step: the original denominators, the
atom polynomials, the square-free product polynomial M, the ordered list of
distinct critical roots (rational values or rational isolating intervals), a
rational sample per open cell, the claimed sign of every atom polynomial on
every cell and at every root, root multiplicities, the domain-exclusion flags
and the claimed solution set.

:func:`verify` trusts *none* of the claimed labels.  It recomputes, from the
polynomial data alone:

* the polynomial identities (atom poly == num*den or num; M == square-free
  part of the product; M square-free via gcd(M, M') == 1);
* each isolating interval (endpoints not roots, exactly one root inside, via
  its own Sturm sequence) and the completeness of the root list (Sturm count
  on the whole line);
* strict ordering of the roots (exact comparisons);
* every cell sign (exact evaluation at the sample, after checking the sample
  lies inside the cell);
* every point sign (its own gcd/Sturm sign determination at the root);
* multiplicities (its own square-free factorization) and the sign-change
  parity across every root;
* the domain-exclusion flags, the boolean evaluation and the resulting set,
  which must equal the claimed solution.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction

from . import polys as P
from .algebraic import AlgebraicNumber, compare_points
from .budget import Budget
from .expr import Atom, BoolOp, op_truth
from .sets import RealSet, build_set


class VerificationError(Exception):
    pass


@dataclass
class CertAtom:
    num: list
    den: list
    op: str
    poly: list  # claimed sign polynomial: num*den for order ops, num for =/!=


@dataclass
class CertRoot:
    kind: str             # 'rational' | 'algebraic'
    value: object = None  # Fraction when rational
    lo: object = None     # rational isolating interval when algebraic
    hi: object = None


@dataclass
class Certificate:
    expr_structure: object
    atoms: list
    denominators: list
    product_poly: list
    roots: list
    cell_samples: list
    cell_signs: list
    point_signs: list
    multiplicities: list
    domain_excluded: list
    claimed: object  # RealSet


def serialize_expr(node):
    if isinstance(node, Atom):
        return ("atom", node.index)
    if node.kind == "not":
        return ("not", serialize_expr(node.children[0]))
    return (node.kind, tuple(serialize_expr(c) for c in node.children))


def eval_structure(node, truths):
    kind = node[0]
    if kind == "atom":
        return truths[node[1]]
    if kind == "and":
        return all(eval_structure(c, truths) for c in node[1])
    if kind == "or":
        return any(eval_structure(c, truths) for c in node[1])
    if kind == "not":
        return not eval_structure(node[1], truths)
    raise VerificationError(f"bad expression node {kind!r}")


def _fail(msg):
    raise VerificationError(msg)


def verify(cert, budget=None):
    """Re-derive every conclusion in ``cert``; raise or return True."""
    budget = budget or Budget()
    atoms = cert.atoms

    # 1. polynomial identities: atom sign polynomials
    for ca in atoms:
        num, den = P.norm(ca.num), P.norm(ca.den)
        expect = P.mul(num, den) if ca.op in ("<", "<=", ">", ">=") else num
        if P.norm(ca.poly) != expect:
            _fail("atom polynomial identity mismatch")

    dens = [P.norm(d) for d in cert.denominators]
    if any(P.is_zero(d) for d in dens):
        if not cert.claimed.is_empty():
            _fail("zero denominator implies empty domain and empty solution")
        return True

    # 2. product polynomial identity and square-freeness
    parts = [P.squarefree_part(ca.poly) for ca in atoms if not P.is_zero(ca.poly)]
    parts += [P.squarefree_part(d) for d in dens]
    acc = [Fraction(1)]
    for s in parts:
        if P.deg(s) >= 1:
            acc = P.mul(acc, s)
    M = P.monic(P.squarefree_part(acc)) if P.deg(acc) >= 1 else [Fraction(1)]
    if P.norm(cert.product_poly) != M:
        _fail("product polynomial identity mismatch")
    if P.deg(M) >= 1 and P.deg(P.gcd(M, P.derivative(M))) >= 1:
        _fail("product polynomial is not square-free")

    # 3. critical roots: validity, ordering, completeness
    pts = []
    for cr in cert.roots:
        if cr.kind == "rational":
            r = Fraction(cr.value)
            if P.evaluate(M, r) != 0:
                _fail("claimed rational root is not a root of M")
            pts.append(r)
        elif cr.kind == "algebraic":
            lo, hi = Fraction(cr.lo), Fraction(cr.hi)
            if not lo < hi:
                _fail("degenerate isolating interval")
            if P.evaluate(M, lo) == 0 or P.evaluate(M, hi) == 0:
                _fail("isolating endpoint is a root of M")
            if P.count_roots(M, lo, hi, budget=budget) != 1:
                _fail("isolating interval does not contain exactly one root")
            pts.append(AlgebraicNumber(M, lo, hi))
        else:
            _fail(f"unknown root kind {cr.kind!r}")
    for i in range(1, len(pts)):
        if compare_points(pts[i - 1], pts[i], budget) >= 0:
            _fail("critical roots are not strictly increasing / not distinct")
    total = P.count_roots(M, P.NINF, P.INF, budget=budget) if P.deg(M) >= 1 else 0
    if total != len(pts):
        _fail("critical root list is incomplete")

    # 4. domain exclusions, recomputed
    excluded = []
    for p in pts:
        ex = False
        for d in dens:
            if isinstance(p, Fraction):
                if P.evaluate(d, p) == 0:
                    ex = True
                    break
            elif p.sign_of_poly_at(d, budget) == 0:
                ex = True
                break
        excluded.append(ex)
    if excluded != list(cert.domain_excluded):
        _fail("domain-exclusion flags mismatch")

    # 5. cell signs, recomputed at verified in-cell samples
    n = len(pts)
    if len(cert.cell_samples) != n + 1:
        _fail("wrong number of cell samples")
    cell_signs = []
    for k in range(n + 1):
        s = Fraction(cert.cell_samples[k])
        if k > 0 and compare_points(pts[k - 1], s, budget) >= 0:
            _fail("cell sample not inside its cell (left)")
        if k < n and compare_points(s, pts[k], budget) >= 0:
            _fail("cell sample not inside its cell (right)")
        row = [P.sign_of_value(P.evaluate(ca.poly, s)) for ca in atoms]
        cell_signs.append(row)
        if row != list(cert.cell_signs[k]):
            _fail("cell sign mismatch")

    # 6. point signs, recomputed independently
    point_signs = []
    for i, p in enumerate(pts):
        row = []
        for ca in atoms:
            if isinstance(p, Fraction):
                row.append(P.sign_of_value(P.evaluate(ca.poly, p)))
            else:
                row.append(p.sign_of_poly_at(ca.poly, budget))
        point_signs.append(row)
        if row != list(cert.point_signs[i]):
            _fail("point sign mismatch")

    # 7. multiplicities and sign-change parity, recomputed
    for j, ca in enumerate(atoms):
        pj = ca.poly
        facts = P.squarefree_factorization(pj) if not P.is_zero(pj) else []
        for i, p in enumerate(pts):
            m = 0
            if point_signs[i][j] == 0 and not P.is_zero(pj):
                for f, k in facts:
                    if P.deg(f) < 1:
                        continue
                    if isinstance(p, Fraction):
                        if P.evaluate(f, p) == 0:
                            m = k
                            break
                    else:
                        g = P.gcd(f, M)
                        if P.deg(g) >= 1 and P.count_roots(
                            g, p.lo, p.hi, budget=budget
                        ) == 1:
                            m = k
                            break
            if m != cert.multiplicities[i][j]:
                _fail("multiplicity mismatch")
            sl, sr = cell_signs[i][j], cell_signs[i + 1][j]
            if m == 0:
                if not (sl == sr == point_signs[i][j]):
                    _fail("sign continuity violated at a non-root")
            else:
                if point_signs[i][j] != 0:
                    _fail("root of multiplicity > 0 has nonzero sign")
                expect = sl if m % 2 == 0 else -sl
                if sr != expect:
                    _fail("sign-change parity violated at a root")

    # 8. boolean evaluation and set reconstruction
    cell_truths = [
        [op_truth(atoms[j].op, cell_signs[k][j]) for j in range(len(atoms))]
        for k in range(n + 1)
    ]
    point_truths = [
        [op_truth(atoms[j].op, point_signs[i][j]) for j in range(len(atoms))]
        for i in range(n)
    ]
    cell_values = [eval_structure(cert.expr_structure, t) for t in cell_truths]
    point_values = [
        eval_structure(cert.expr_structure, point_truths[i]) and not excluded[i]
        for i in range(n)
    ]
    rebuilt = build_set(pts, cell_values, point_values, budget)
    if rebuilt != cert.claimed:
        _fail("claimed solution set does not match the verified derivation")
    return True
