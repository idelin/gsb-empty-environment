"""Rational functions over Q with exact domain analysis.

A :class:`RationalFunc` is stored in *reduced* form ``num/den`` with
``gcd(num, den) = 1`` and a monic denominator.  The original (unreduced)
denominator is kept so that removable singularities — points excluded from
the domain even though the reduced function extends continuously — are
reported exactly.  Every construction step records the domain on which the
transformation is valid.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction

from .poly import Poly, poly_gcd, squarefree_part
from .roots import RootAnalysis, analyze_roots, DEFAULT_BUDGET


@dataclass(frozen=True)
class SideLimit:
    """One-sided limit evidence at a point.

    ``kind`` is one of ``'value'`` (finite, exact), ``'pole'`` (infinite,
    with ``sign`` giving the direction) or ``'undecided'`` (the budget was
    exhausted before an algebraic decision could be reached).
    """

    kind: str
    value: Fraction | None = None
    sign: int | None = None
    evidence: tuple[str, ...] = ()


def _exact_div(a: Poly, b: Poly) -> Poly:
    from .poly import divmod_poly
    quotient, remainder = divmod_poly(a, b)
    if not remainder.is_zero():
        raise ArithmeticError("internal error: inexact polynomial division")
    return quotient


@dataclass
class RationalFunc:
    num: Poly
    den: Poly
    original_den: Poly
    steps: list[str] = field(default_factory=list)
    _den_roots: dict = field(default_factory=dict, repr=False)
    _hole_roots: dict = field(default_factory=dict, repr=False)

    def __init__(self, num: Poly, den: Poly, note: str | None = None,
                 original_den: Poly | None = None):
        if den.is_zero():
            raise ZeroDivisionError("rational function with zero denominator")
        common = poly_gcd(num, den)
        reduced_num = _exact_div(num, common)
        reduced_den = _exact_div(den, common).monic()
        # num/den = reduced_num / (reduced_den * lead(den)/lead(common))
        scale = common.leading() / den.leading()
        reduced_num = reduced_num.scale(scale)
        self.num = reduced_num
        self.den = reduced_den
        self.original_den = original_den if original_den is not None else den
        self._den_roots = {}
        self._hole_roots = {}
        self.steps = []
        if note:
            self.steps.append(note)
        if common.degree > 0:
            self.steps.append(
                f"约去公因式 ({common.to_human()})：在除去其零点的定义域上恒等，"
                "这些零点成为可去奇点"
            )
        self.steps.append(
            f"分母首一化：等价变形，在原定义域 D = {{x : ({den.to_human()}) != 0}} 上成立"
        )

    @staticmethod
    def constant(value) -> "RationalFunc":
        return RationalFunc(Poly.const(value), Poly.const(1),
                            note="常值函数：在全体实数上有定义")

    @staticmethod
    def identity() -> "RationalFunc":
        return RationalFunc(Poly.x(), Poly.const(1),
                            note="恒等函数：在全体实数上有定义")

    def _combine(self, other: "RationalFunc", op: str) -> "RationalFunc":
        if op == "add":
            num = self.num * other.den + other.num * self.den
            rule = "通分相加 a/b + c/d = (ad + bc)/(bd)"
        elif op == "sub":
            num = self.num * other.den - other.num * self.den
            rule = "通分相减 a/b - c/d = (ad - bc)/(bd)"
        else:
            num = self.num * other.num
            rule = "分子分母分别相乘 (a/b)*(c/d) = (ac)/(bd)"
        den = self.den * other.den
        # Domain of the combination is the intersection of the operand
        # domains, so the unreduced denominators multiply as well.
        original_den = self.original_den * other.original_den
        result = RationalFunc(num, den, original_den=original_den)
        result.steps = [
            f"{rule}：在两函数定义域的交集上成立",
            *self.steps,
            *other.steps,
            *result.steps,
        ]
        return result

    def __add__(self, other: "RationalFunc") -> "RationalFunc":
        return self._combine(other, "add")

    def __sub__(self, other: "RationalFunc") -> "RationalFunc":
        return self._combine(other, "sub")

    def __mul__(self, other: "RationalFunc") -> "RationalFunc":
        return self._combine(other, "mul")

    def den_root_analysis(self, budget: int = DEFAULT_BUDGET) -> RootAnalysis:
        if budget not in self._den_roots:
            self._den_roots[budget] = analyze_roots(self.den, budget)
        return self._den_roots[budget]

    def hole_root_analysis(self, budget: int = DEFAULT_BUDGET) -> RootAnalysis:
        """Real roots of the cancelled factor (removable singularities)."""
        if budget not in self._hole_roots:
            common = _exact_div(self.original_den, self.den)
            if common.degree == 0:
                self._hole_roots[budget] = RootAnalysis()
            else:
                self._hole_roots[budget] = analyze_roots(common, budget)
        return self._hole_roots[budget]

    def rational_exclusions(self) -> list[Fraction]:
        """All rational points excluded from the domain (poles + removable)."""
        points = {r.value for r in self.den_root_analysis().rational}
        points |= {r.value for r in self.hole_root_analysis().rational}
        return sorted(points)

    def is_defined_at(self, point) -> bool:
        return self.original_den.eval(Fraction(point)) != 0

    def eval(self, point) -> Fraction:
        """Exact value at a rational point of the domain."""
        point = Fraction(point)
        if not self.is_defined_at(point):
            raise ZeroDivisionError(f"{point} 不在定义域内")
        return self.num.eval(point) / self.den.eval(point)

    def value_or_limit(self, point) -> SideLimit:
        """Two-sided limit at a rational point, with algebraic evidence."""
        point = Fraction(point)
        if self.is_defined_at(point):
            return SideLimit("value", value=self.eval(point),
                             evidence=(f"{point} 在定义域内，直接代入",))
        den_val = self.den.eval(point)
        if den_val == 0:
            return SideLimit("pole", evidence=(
                f"约化后分母在 {point} 处为 0（分子由互素性非 0），故为极点",))
        return SideLimit("value", value=self.num.eval(point) / den_val,
                         evidence=(
                             f"原分母在 {point} 处为 0 但约化分母非 0，属可去奇点",
                             "对约化后的分式直接代入求得极限",
                         ))

    def side_limits(self, point, budget: int = DEFAULT_BUDGET) -> tuple[SideLimit, SideLimit]:
        """Exact left/right limits at a rational point.

        For poles the sign on each side is decided by the parity of the
        multiplicity of ``(x - point)`` in numerator and denominator — a
        purely algebraic argument, no numeric approximation involved.
        """
        point = Fraction(point)
        two_sided = self.value_or_limit(point)
        if two_sided.kind != "pole":
            return two_sided, two_sided
        num_q, num_mult = self.num.factor_linear(point)
        den_q, den_mult = self.den.factor_linear(point)
        base_sign = 1 if num_q.eval(point) * den_q.eval(point) > 0 else -1
        order = den_mult - num_mult  # > 0 because this is a pole
        left_sign = base_sign * ((-1) ** order)
        right_sign = base_sign
        evidence = (
            f"(x - {point}) 在分子中重数 {num_mult}、分母中重数 {den_mult}，极点阶数 {order}",
            f"约化部分在 {point} 处符号 {'+' if base_sign > 0 else '-'}；"
            f"左侧 (x - {point}) < 0，右侧 > 0",
        )
        return (SideLimit("pole", sign=left_sign, evidence=evidence),
                SideLimit("pole", sign=right_sign, evidence=evidence))

    def __eq__(self, other) -> bool:
        return (isinstance(other, RationalFunc)
                and self.num == other.num and self.den == other.den
                and self.domain_fingerprint() == other.domain_fingerprint())

    def __hash__(self) -> int:
        return hash((self.num, self.den, self.domain_fingerprint()))

    def domain_fingerprint(self) -> Poly:
        """Square-free part of the original denominator: exact zero-set key."""
        return squarefree_part(self.original_den)

    def to_human(self) -> str:
        if self.den.degree == 0 and self.den.coeffs[0] == 1:
            return self.num.to_human()
        return f"({self.num.to_human()}) / ({self.den.to_human()})"

    def __repr__(self) -> str:
        return f"RationalFunc({self.to_human()})"
