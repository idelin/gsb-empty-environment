"""Exact solver for boolean combinations of univariate rational constraints.

Semantics: all real zeros of *every original denominator* are excluded from
the common domain first (cancellation never restores them); the boolean
formula is then evaluated on that domain, so NOT never re-admits an excluded
point.

The derivation is decomposed into reviewable steps (see :class:`SolveSteps`):
domain exclusion set, ordered distinct critical roots, signs on every open
cell and endpoint membership.  A :class:`~exactsets.certificate.Certificate`
is emitted for independent verification.

Budget exhaustion never produces a completeness claim: a ``partial`` Solution
carries the certified roots found so far and the pending isolating intervals.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction

from . import polys as P
from .algebraic import AlgebraicNumber, isolate_all, rational_between, sort_points
from .budget import Budget, BudgetExceeded
from .certificate import CertAtom, CertRoot, Certificate, serialize_expr
from .expr import Atom, BoolOp, iter_atoms, op_truth
from .sets import RealSet, build_set


@dataclass
class SolveSteps:
    denominators: list = field(default_factory=list)
    atom_polys: list = field(default_factory=list)
    product_poly: object = None
    critical_roots: list = field(default_factory=list)
    cell_samples: list = field(default_factory=list)
    cell_signs: list = field(default_factory=list)
    point_signs: list = field(default_factory=list)
    multiplicities: list = field(default_factory=list)
    domain_excluded: list = field(default_factory=list)
    cell_values: list = field(default_factory=list)
    point_values: list = field(default_factory=list)

    def summary(self):
        lines = []
        lines.append(
            "denominators: " + ", ".join(P.poly_to_str(d) for d in self.denominators)
        )
        lines.append("product poly M: " + P.poly_to_str(self.product_poly or [1]))
        roots = []
        for r in self.critical_roots:
            roots.append(str(r) if isinstance(r, Fraction) else repr(r))
        lines.append("critical roots: " + (", ".join(roots) or "(none)"))
        for k, s in enumerate(self.cell_samples):
            lines.append(f"  cell {k}: sample={s} signs={self.cell_signs[k]} "
                         f"value={self.cell_values[k]}")
        for i, r in enumerate(self.critical_roots):
            lines.append(
                f"  point {r}: signs={self.point_signs[i]} "
                f"mults={self.multiplicities[i]} "
                f"excluded={self.domain_excluded[i]} member={self.point_values[i]}"
            )
        return "\n".join(lines)


@dataclass
class Solution:
    status: str                    # 'complete' | 'partial'
    set: object = None             # RealSet when complete
    steps: object = None
    certificate: object = None
    proven_roots: list = field(default_factory=list)
    pending: list = field(default_factory=list)  # (lo, hi, root_count) or None
    note: str = ""


def _eval_formula(node, truths):
    if isinstance(node, Atom):
        return truths[node.index]
    if node.kind == "and":
        return all(_eval_formula(c, truths) for c in node.children)
    if node.kind == "or":
        return any(_eval_formula(c, truths) for c in node.children)
    if node.kind == "not":
        return not _eval_formula(node.children[0], truths)
    raise TypeError(node)


def solve(expr, budget=None):
    budget = budget or Budget()
    atoms = list(iter_atoms(expr))
    for i, a in enumerate(atoms):
        a.index = i
    dens = [a.den for a in atoms]

    if any(P.is_zero(d) for d in dens):
        steps = SolveSteps(denominators=dens)
        return Solution(
            "complete",
            set=RealSet.empty(),
            steps=steps,
            note="a denominator is the zero polynomial: the domain is empty",
        )

    atom_polys = [
        P.mul(a.num, a.den) if a.op in ("<", "<=", ">", ">=") else a.num
        for a in atoms
    ]

    parts = [P.squarefree_part(p) for p in atom_polys if not P.is_zero(p)]
    parts += [P.squarefree_part(d) for d in dens]
    acc = [Fraction(1)]
    for s in parts:
        if P.deg(s) >= 1:
            acc = P.mul(acc, s)
    M = P.monic(P.squarefree_part(acc)) if P.deg(acc) >= 1 else [Fraction(1)]

    # -- isolate all distinct critical roots -------------------------------
    try:
        if P.deg(M) >= 1:
            raw = isolate_all(M, budget)
            algebraics = [r for r in raw if isinstance(r, AlgebraicNumber)]
            rationals = [r for r in raw if isinstance(r, Fraction)]
            for a in algebraics:
                for r in rationals:
                    while a.lo <= r <= a.hi:
                        a.refine(budget)
            roots = sort_points(
                [AlgebraicNumber(M, a.lo, a.hi) for a in algebraics] + rationals,
                budget,
            )
        else:
            roots = []
    except BudgetExceeded as e:
        pending = [t for t in (e.frontier or []) if t[2] is None or t[2] >= 1]
        if not pending:
            pending = [(None, None, None)]
        return Solution(
            "partial",
            proven_roots=list(e.found or []),
            pending=pending,
            note=f"budget exhausted during root isolation: {e}",
        )

    n = len(roots)
    try:
        samples = [
            rational_between(
                roots[k - 1] if k > 0 else None,
                roots[k] if k < n else None,
                budget,
            )
            for k in range(n + 1)
        ]
        cell_signs = [
            [P.sign_of_value(P.evaluate(pj, s)) for pj in atom_polys]
            for s in samples
        ]
        point_signs = []
        for r in roots:
            if isinstance(r, Fraction):
                point_signs.append(
                    [P.sign_of_value(P.evaluate(pj, r)) for pj in atom_polys]
                )
            else:
                point_signs.append(
                    [r.sign_of_poly_at(pj, budget) for pj in atom_polys]
                )
        excluded = []
        for r in roots:
            ex = False
            for d in dens:
                if isinstance(r, Fraction):
                    if P.evaluate(d, r) == 0:
                        ex = True
                        break
                elif r.sign_of_poly_at(d, budget) == 0:
                    ex = True
                    break
            excluded.append(ex)
        # multiplicities via square-free factorization (sign-change parity)
        factors = [
            P.squarefree_factorization(pj) if not P.is_zero(pj) else []
            for pj in atom_polys
        ]
        mults = []
        for i, r in enumerate(roots):
            row = []
            for j, pj in enumerate(atom_polys):
                m = 0
                if point_signs[i][j] == 0 and not P.is_zero(pj):
                    for f, k in factors[j]:
                        if P.deg(f) < 1:
                            continue
                        if isinstance(r, Fraction):
                            if P.evaluate(f, r) == 0:
                                m = k
                                break
                        else:
                            g = P.gcd(f, M)
                            if P.deg(g) >= 1 and P.count_roots(
                                g, r.lo, r.hi, budget=budget
                            ) == 1:
                                m = k
                                break
                row.append(m)
            mults.append(row)
    except BudgetExceeded as e:
        return Solution(
            "partial",
            proven_roots=roots,
            pending=[],
            note=f"budget exhausted during sign determination: {e}",
        )

    # solver-side consistency: sign changes must match root multiplicity
    for i in range(n):
        for j in range(len(atom_polys)):
            m = mults[i][j]
            sl, sr = cell_signs[i][j], cell_signs[i + 1][j]
            if m == 0:
                assert sl == sr == point_signs[i][j], "sign continuity failed"
            else:
                assert point_signs[i][j] == 0
                assert sr == (sl if m % 2 == 0 else -sl), "sign parity failed"

    cell_truths = [
        [op_truth(atoms[j].op, cell_signs[k][j]) for j in range(len(atoms))]
        for k in range(n + 1)
    ]
    point_truths = [
        [op_truth(atoms[j].op, point_signs[i][j]) for j in range(len(atoms))]
        for i in range(n)
    ]
    cell_values = [_eval_formula(expr, t) for t in cell_truths]
    point_values = [
        _eval_formula(expr, point_truths[i]) and not excluded[i] for i in range(n)
    ]
    result = build_set(roots, cell_values, point_values, budget)

    steps = SolveSteps(
        denominators=dens,
        atom_polys=atom_polys,
        product_poly=M,
        critical_roots=roots,
        cell_samples=samples,
        cell_signs=cell_signs,
        point_signs=point_signs,
        multiplicities=mults,
        domain_excluded=excluded,
        cell_values=cell_values,
        point_values=point_values,
    )
    cert = Certificate(
        expr_structure=serialize_expr(expr),
        atoms=[
            CertAtom(a.num, a.den, a.op, ap) for a, ap in zip(atoms, atom_polys)
        ],
        denominators=dens,
        product_poly=M,
        roots=[
            CertRoot("rational", value=r)
            if isinstance(r, Fraction)
            else CertRoot("algebraic", lo=r.lo, hi=r.hi)
            for r in roots
        ],
        cell_samples=samples,
        cell_signs=cell_signs,
        point_signs=point_signs,
        multiplicities=mults,
        domain_excluded=excluded,
        claimed=result,
    )
    return Solution("complete", set=result, steps=steps, certificate=cert)
