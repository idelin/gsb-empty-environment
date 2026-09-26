"""有理式 p/q：定义域空洞、可去奇点、单侧极限，全部精确。

约定：
- self.den（书写分母）决定定义域：den(x)=0 处函数无定义（定义域空洞）；
- 约去 gcd 后的 q1 决定极限行为：q1(x)=0 处为极点（极限无穷）；
- gcd 的零点中非极点者为可去奇点（极限存在但原式无定义）。
每个变换步骤在 trace 中记录其适用域。
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .poly import Poly
from .roots import IsolationBudget, RootIsolation, isolate_roots


@dataclass(frozen=True)
class Limit:
    """极限值：有限（精确 Fraction）或无穷（带符号）。"""

    kind: str                 # 'finite' | 'infinite'
    value: Fraction | None = None
    sign: int | None = None   # infinite: +1 / -1 / None（两侧异号）

    @property
    def is_finite(self) -> bool:
        return self.kind == 'finite'

    def __str__(self):
        if self.is_finite:
            return str(self.value)
        return {1: '+∞', -1: '-∞', None: '∞'}[self.sign]


class RationalFunction:
    def __init__(self, num, den=Poly.one(), trace=()):
        num = num if isinstance(num, Poly) else Poly(num)
        den = den if isinstance(den, Poly) else Poly(den)
        if den.is_zero():
            raise ValueError("分母不能为零多项式")
        self.num = num
        self.den = den
        self.trace = list(trace)
        self._g = self._p1 = self._q1 = None

    @classmethod
    def of(cls, num, den=Poly.one(), label='f') -> "RationalFunction":
        f = cls(num, den)
        f.trace.append(
            f"定义 {label} = ({f.num})/({f.den})；适用域：分母 ({f.den}) ≠ 0"
            if f.den.degree >= 1 else
            f"定义 {label} = {f.num}（多项式，处处有定义）"
        )
        return f

    # ---- 约简 ----
    def _ensure_simplified(self):
        if self._g is None:
            g = self.num.gcd(self.den)
            self._g = g
            self._p1 = self.num // g
            self._q1 = self.den // g
            if g.degree >= 1:
                self.trace.append(
                    f"约去公因子 ({g})：等式仅在 ({g}) ≠ 0 处成立；"
                    f"其零点为可去奇点或极点，需单独考察"
                )

    @property
    def common_factor(self) -> Poly:
        self._ensure_simplified()
        return self._g

    @property
    def simplified_num(self) -> Poly:
        self._ensure_simplified()
        return self._p1

    @property
    def simplified_den(self) -> Poly:
        self._ensure_simplified()
        return self._q1

    def removable_factor(self) -> Poly:
        """其零点恰为全部可去奇点的多项式（g 中去掉 q1 的零点）。"""
        self._ensure_simplified()
        rem = self._g
        while rem.degree >= 1:
            d = rem.gcd(self._q1)
            if d.degree < 1:
                break
            rem = rem // d
        return rem

    # ---- 求值与极限 ----
    def value_at(self, x) -> Fraction | None:
        """原式在 x 处的值；分母为零（定义域空洞）时返回 None。"""
        x = x if isinstance(x, Fraction) else Fraction(x)
        d = self.den.eval(x)
        if d == 0:
            return None
        return self.num.eval(x) / d

    def is_defined_at(self, x) -> bool:
        return self.value_at(x) is not None

    def extended_value_at(self, x) -> Fraction | None:
        """连续延拓值（用约简式）；极点处返回 None。"""
        x = x if isinstance(x, Fraction) else Fraction(x)
        self._ensure_simplified()
        q = self._q1.eval(x)
        if q == 0:
            return None
        return self._p1.eval(x) / q

    def limit_at(self, x, side: str = 'both') -> Limit:
        """x 处单侧/双侧极限，精确计算。

        side: 'left' | 'right' | 'both'。极点处按分母零点重数与符号
        精确判定 ±∞；两侧异号时双侧极限记为无符号 ∞。
        """
        x = x if isinstance(x, Fraction) else Fraction(x)
        self._ensure_simplified()
        q = self._q1.eval(x)
        if q != 0:
            return Limit('finite', self._p1.eval(x) / q)
        m, r = self._q1.deflate(x)
        s = self._p1.sign_at(x) * r.sign_at(x)
        right = s
        left = s if m % 2 == 0 else -s
        if side == 'left':
            return Limit('infinite', sign=left)
        if side == 'right':
            return Limit('infinite', sign=right)
        return Limit('infinite', sign=left if left == right else None)

    # ---- 定义域分析 ----
    def poles(self, lo=None, hi=None, budget: IsolationBudget | None = None) -> RootIsolation:
        """(lo, hi] 内极点（q1 的实根）的隔离结果。"""
        return isolate_roots(self.simplified_den, lo, hi, budget)

    def removable_points(self, lo=None, hi=None, budget: IsolationBudget | None = None) -> RootIsolation:
        """(lo, hi] 内可去奇点的隔离结果。"""
        return isolate_roots(self.removable_factor(), lo, hi, budget)

    # ---- 运算（保留书写分母以维持定义域；trace 记录适用域）----
    def __add__(self, other: "RationalFunction") -> "RationalFunction":
        num = self.num * other.den + other.num * self.den
        den = self.den * other.den
        trace = self.trace + other.trace + [
            f"相加：({self}) + ({other})；适用域为两者定义域之交，"
            f"任一分母零点处仍无定义"
        ]
        return RationalFunction(num, den, trace)

    def __sub__(self, other: "RationalFunction") -> "RationalFunction":
        return self + (-other)

    def __neg__(self) -> "RationalFunction":
        return RationalFunction(-self.num, self.den, self.trace + ["取负：定义域不变"])

    def __mul__(self, other: "RationalFunction") -> "RationalFunction":
        num = self.num * other.num
        den = self.den * other.den
        trace = self.trace + other.trace + [
            f"相乘：({self}) * ({other})；适用域为两者定义域之交，"
            f"约简不回收定义域空洞"
        ]
        return RationalFunction(num, den, trace)

    # ---- 比较与展示 ----
    def _canonical(self):
        self._ensure_simplified()
        lc = self._q1.leading_coeff()
        return self._p1 * (1 / lc), self._q1 * (1 / lc)

    def __eq__(self, other):
        return isinstance(other, RationalFunction) and self._canonical() == other._canonical()

    def __str__(self):
        if self.den.is_constant() and self.den.coeffs[0] == 1:
            return str(self.num)
        return f"({self.num})/({self.den})"

    __repr__ = __str__
