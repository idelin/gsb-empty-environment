"""有理函数（多项式分式）的 GCD 约分与定义域管理。

设计要点：
- 定义域保存为【原分母】的实根排除集合，约分不丢失可去奇点；
- 排除集合元素：Fraction（有理根，精确）或 (Fraction, Fraction)
  （无理根的有理隔离区间，开区间，含唯一实根）；
- 分母首项系数规范为首一（隐含首项符号规范）；
- 每步变换在 history 中记录说明及其适用域；
- 零分母拒绝构造；零函数化简为 0/1 但保留排除集合。
"""
from __future__ import annotations

from fractions import Fraction

from polyutil import (
    add as padd, sub as psub, mul as pmul, scale, poly, is_zero,
    gcd as pgcd, div_exact, leading_coeff, evaluate as peval,
    isolate_real_roots, fmt_poly,
)


class DomainError(ZeroDivisionError):
    """在原分母的实根（含可去奇点）处求值时抛出。"""


def normalize_exclusions(items):
    """排序、合并重叠区间、去重点、丢弃落在区间内的点。"""
    points = set()
    intervals = []
    for item in items:
        if isinstance(item, tuple):
            lo, hi = Fraction(item[0]), Fraction(item[1])
            if not lo < hi:
                raise ValueError(f"非法隔离区间：{item}")
            intervals.append((lo, hi))
        else:
            points.add(Fraction(item))
    intervals.sort()
    merged = []
    for lo, hi in intervals:
        if merged and lo <= merged[-1][1]:
            merged[-1] = (merged[-1][0], max(merged[-1][1], hi))
        else:
            merged.append((lo, hi))
    result = [pt for pt in points
              if not any(lo < pt < hi for lo, hi in merged)]
    result.extend(merged)
    result.sort(key=lambda it: (it[0] if isinstance(it, tuple) else it,
                                it[1] if isinstance(it, tuple) else it))
    return result


def is_excluded(exclusions, x):
    x = Fraction(x)
    for item in exclusions:
        if isinstance(item, tuple):
            if item[0] < x < item[1]:
                return True
        elif item == x:
            return True
    return False


def fmt_exclusions(exclusions):
    if not exclusions:
        return "无（定义域为全体实数）"
    parts = []
    for item in exclusions:
        if isinstance(item, tuple):
            lo, hi = item
            parts.append(f"x ∈ ({float(lo):.6f}, {float(hi):.6f})"
                         f"〔无理根，有理隔离区间，宽 {float(hi - lo):.2e}〕")
        else:
            parts.append(f"x = {item}")
    return "; ".join(parts)


class RationalFunction:
    """有理函数 num/den，附带定义域排除集合与变换历史。"""

    def __init__(self, num, den, *, exclusions=None, history=None):
        num = poly(num)
        den = poly(den)
        if is_zero(den):
            raise ValueError("零分母：拒绝构造（分母为零多项式）")
        self.original_num = num
        self.original_den = den
        if exclusions is None:
            exclusions = isolate_real_roots(den)
        self.exclusions = normalize_exclusions(exclusions)
        self.history = list(history) if history else []
        self.history.append(
            f"构造 f(x) = ({fmt_poly(num)}) / ({fmt_poly(den)})。"
            f"适用域：R 减去原分母实根，即排除 {{{fmt_exclusions(self.exclusions)}}}"
        )
        self._simplify()

    def _simplify(self):
        num, den = self.original_num, self.original_den
        if is_zero(num):
            self.num, self.den = poly([]), poly([1])
            self.history.append(
                "分子为零多项式：零函数，化简为 0/1。"
                "适用域不变：排除集合保留原分母实根（零函数仍有定义域限制）"
            )
            return
        g = pgcd(num, den)
        n1 = div_exact(num, g)
        d1 = div_exact(den, g)
        lead = leading_coeff(d1)
        n1 = scale(n1, 1 / lead)
        d1 = scale(d1, 1 / lead)
        self.num, self.den = n1, d1
        steps = []
        if is_zero(g) or len(g) > 1 or g[0] != 1:
            steps.append(f"约去公因子 g(x) = {fmt_poly(g)}")
        else:
            steps.append("分子分母互素，无需约分")
        if lead != 1:
            steps.append(f"分母首项系数规范为首一（同乘 {1 / lead}，首项符号规范）")
        self.history.append(
            "约分：" + "；".join(steps) +
            f"，得 ({fmt_poly(n1)}) / ({fmt_poly(d1)})。"
            "适用域不变：等式仅在排除集合外成立，可去奇点仍被排除"
        )

    # ---- 求值 ----
    def evaluate(self, x):
        x = Fraction(x)
        if is_excluded(self.exclusions, x):
            raise DomainError(f"x = {x} 被排除（原分母的实根，含可去奇点）")
        return peval(self.num, x) / peval(self.den, x)

    def evaluate_original(self, x):
        """按原始未约分形式求值（用于对照验证）。"""
        x = Fraction(x)
        if is_excluded(self.exclusions, x):
            raise DomainError(f"x = {x} 被排除（原分母的实根）")
        return peval(self.original_num, x) / peval(self.original_den, x)

    # ---- 组合运算：排除集合取并集后重新约分 ----
    def _combine(self, other, op):
        other = as_rational_function(other)
        if op == "+":
            num = padd(pmul(self.num, other.den), pmul(other.num, self.den))
        else:
            num = psub(pmul(self.num, other.den), pmul(other.num, self.den))
        den = pmul(self.den, other.den)
        exclusions = self.exclusions + other.exclusions
        history = (self.history + other.history + [
            f"组合运算（{op}）：通分后排除集合取两者并集再约分。"
            "适用域：两操作数定义域之交"
        ])
        return RationalFunction(num, den, exclusions=exclusions, history=history)

    def __add__(self, other):
        return self._combine(other, "+")

    __radd__ = __add__

    def __sub__(self, other):
        return self._combine(other, "-")

    def __rsub__(self, other):
        return as_rational_function(other)._combine(self, "-")

    def __mul__(self, other):
        other = as_rational_function(other)
        num = pmul(self.num, other.num)
        den = pmul(self.den, other.den)
        exclusions = self.exclusions + other.exclusions
        history = (self.history + other.history + [
            "组合运算（*）：分子分母分别相乘，排除集合取并集再约分。"
            "适用域：两操作数定义域之交"
        ])
        return RationalFunction(num, den, exclusions=exclusions, history=history)

    __rmul__ = __mul__

    def __truediv__(self, other):
        other = as_rational_function(other)
        if is_zero(other.num):
            raise ValueError("除以零函数：拒绝")
        inv_exclusions = other.exclusions + isolate_real_roots(other.num)
        inv = RationalFunction(
            other.den, other.num, exclusions=inv_exclusions,
            history=other.history + [
                "取倒数：分子分母互换，原分子的实根加入排除集合。"
                "适用域：原定义域再排除原分子的实根"
            ])
        return self * inv

    def __rtruediv__(self, other):
        return as_rational_function(other).__truediv__(self)

    def __repr__(self):
        return (f"RationalFunction(({fmt_poly(self.num)}) / ({fmt_poly(self.den)}), "
                f"排除: {fmt_exclusions(self.exclusions)})")


def as_rational_function(value):
    if isinstance(value, RationalFunction):
        return value
    return RationalFunction([Fraction(value)], [1])
