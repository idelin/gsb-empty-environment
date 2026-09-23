"""polysub —— 多变量多项式的同时代换（纯标准库，精确有理数）。

设计要点
========
- 系数一律使用 fractions.Fraction，全程精确有理数运算，不引入浮点误差，
  也不依赖任何符号代数库（如 sympy）。
- 代换是“同时”的：先构造完整代换映射，再对每个单项式一次性求值，
  替换式中的变量不会被二次代换，因此 {x: y, y: x} 是交换而非污染。
- 展开受项数预算控制：先按上界估计展开后的项数，超出预算则保留
  未展开的 Expr 表达式树（精确值的符号形式），绝不返回截断的假等式。
"""

from __future__ import annotations

from fractions import Fraction
from numbers import Integral, Rational

#: 项数估计的上限。估计只用于与预算比较，超过此值即视为“爆炸”，
#: 避免对巨大二项式系数做无谓的精确计算。
ESTIMATE_CAP = 10 ** 12


def _cap(value: int) -> int:
    return value if value <= ESTIMATE_CAP else ESTIMATE_CAP


def _capped_comb(n: int, k: int, cap: int = ESTIMATE_CAP) -> int:
    """C(n, k)，结果超过 cap 时提前截断返回 cap。

    适用域：n >= 0 的整数；k < 0 或 k > n 时按组合数定义返回 0。
    逐步乘除保持每步为精确整数，且结果一旦超过 cap 立即停算，
    因此循环次数有界（cap 以内最多约 40 步）。
    """
    if k < 0 or k > n:
        return 0
    k = min(k, n - k)
    result = 1
    for i in range(1, k + 1):
        result = result * (n - k + i) // i  # 每步恰为 C(n-k+i, i)，整除精确
        if result > cap:
            return cap
    return result


class BudgetExceeded(Exception):
    """展开预算不足：展开被中止，未产生任何截断结果。

    适用域：仅在 Expr.expand(budget=...) 中估计项数超过预算时抛出。
    异常携带未展开的 expr，调用方可用更大预算重试；
    库保证任何路径都不会返回“部分展开”的多项式假等式。
    """

    def __init__(self, estimate: int, budget: int, expr: "Expr"):
        self.estimate = estimate
        self.budget = budget
        self.expr = expr
        super().__init__(
            f"展开估计需要 {estimate} 项，超出预算 {budget} 项；已保留未展开形式"
        )


def _as_poly(value, variables=()) -> "Poly":
    """把 int/Fraction/Poly 规范化为 Poly。

    适用域：value 为 Poly 原样返回；为有理数（int/Fraction）时构造常数
    多项式；其他类型抛 TypeError（本库不处理浮点，避免精度污染）。
    """
    if isinstance(value, Poly):
        return value
    if isinstance(value, bool) or not isinstance(value, (Integral, Rational)):
        raise TypeError(f"无法转换为多项式: {type(value).__name__}")
    return Poly.constant(Fraction(value), variables)


class Poly:
    """多元多项式，系数为 Fraction（精确有理数）。

    表示：terms 为 {单项式元组: 系数}，单项式元组与 variables 对齐，
    例如 variables=("x", "y") 时 (2, 1) 表示 x^2*y。零多项式 terms 为空。
    所有运算自动对齐变量集合（排序后的并集）。
    """

    __slots__ = ("variables", "terms")

    def __init__(self, variables=(), terms=None):
        self.variables = tuple(sorted(variables))
        self.terms: dict[tuple[int, ...], Fraction] = {}
        for mono, coeff in (terms or {}).items():
            if len(mono) != len(self.variables):
                raise ValueError("单项式长度与变量数不一致")
            coeff = Fraction(coeff)
            if coeff:
                self.terms[tuple(mono)] = coeff

    # ---- 构造 ----

    @classmethod
    def constant(cls, value, variables=()) -> "Poly":
        """常数多项式。适用域：value 为任意有理数（int/Fraction）。"""
        value = Fraction(value)
        terms = {(0,) * len(tuple(variables)): value} if value else {}
        return cls(variables, terms)

    @classmethod
    def var(cls, name: str, variables=None) -> "Poly":
        """单变量单项式 x_i。variables 缺省时变量集合即 {name}。"""
        variables = tuple(sorted(variables)) if variables else (name,)
        if name not in variables:
            raise ValueError(f"{name!r} 不在变量集合 {variables} 中")
        mono = tuple(1 if v == name else 0 for v in variables)
        return cls(variables, {mono: Fraction(1)})

    @classmethod
    def zero(cls, variables=()) -> "Poly":
        return cls(variables)

    # ---- 基本性质 ----

    @property
    def num_terms(self) -> int:
        return len(self.terms)

    def is_zero(self) -> bool:
        return not self.terms

    def total_degree(self) -> int:
        """总次数。适用域：零多项式按约定返回 -1。"""
        return max((sum(m) for m in self.terms), default=-1)

    # ---- 变量对齐 ----

    def _project(self, variables) -> "Poly":
        """把多项式投影到新的变量集合（必须包含原变量）。"""
        variables = tuple(sorted(variables))
        if variables == self.variables:
            return self
        pos = {v: i for i, v in enumerate(self.variables)}
        terms = {}
        for mono, coeff in self.terms.items():
            new_mono = tuple(mono[pos[v]] if v in pos else 0 for v in variables)
            terms[new_mono] = coeff
        return Poly(variables, terms)

    def _aligned(self, other) -> tuple["Poly", "Poly"]:
        other = _as_poly(other, self.variables)
        variables = tuple(sorted(set(self.variables) | set(other.variables)))
        return self._project(variables), other._project(variables)

    # ---- 运算（均在多项式环内，适用域：右操作数为 Poly 或有理数） ----

    def __add__(self, other) -> "Poly":
        a, b = self._aligned(other)
        terms = dict(a.terms)
        for mono, coeff in b.terms.items():
            terms[mono] = terms.get(mono, Fraction(0)) + coeff
        return Poly(a.variables, terms)

    __radd__ = __add__

    def __neg__(self) -> "Poly":
        return Poly(self.variables, {m: -c for m, c in self.terms.items()})

    def __sub__(self, other) -> "Poly":
        return self + (-_as_poly(other, self.variables))

    def __rsub__(self, other) -> "Poly":
        return _as_poly(other, self.variables) + (-self)

    def __mul__(self, other) -> "Poly":
        a, b = self._aligned(other)
        terms: dict[tuple[int, ...], Fraction] = {}
        for ma, ca in a.terms.items():
            for mb, cb in b.terms.items():
                mono = tuple(x + y for x, y in zip(ma, mb))
                terms[mono] = terms.get(mono, Fraction(0)) + ca * cb
        return Poly(a.variables, terms)

    __rmul__ = __mul__

    def __pow__(self, n) -> "Poly":
        """非负整数次幂（反复平方）。

        适用域：仅非负整数指数；负指数属于有理函数域，不在多项式环内，
        本库不支持。0^0 按空积约定为 1。
        """
        if isinstance(n, bool) or not isinstance(n, Integral) or n < 0:
            raise ValueError("仅支持非负整数次幂（多项式环内运算）")
        result = Poly.constant(1, self.variables)
        base = self
        n = int(n)
        while n:
            if n & 1:
                result = result * base
            base = base * base
            n >>= 1
        return result

    def __eq__(self, other) -> bool:
        if not isinstance(other, Poly):
            try:
                other = _as_poly(other, self.variables)
            except TypeError:
                return NotImplemented
        variables = tuple(sorted(set(self.variables) | set(other.variables)))
        return self._project(variables).terms == other._project(variables).terms

    __hash__ = None  # 多项式可变语义下不提供哈希

    # ---- 同时代换 ----

    def substitute(self, mapping: dict, budget: int | None = None):
        """同时代换：mapping 中所有变量一次性替换，而非顺序替换。

        适用域：
        - mapping 的键必须是 self.variables 中的变量名，否则抛 ValueError；
        - 值可以是 Poly / int / Fraction，一律先规范化为 Poly；
        - “同时”语义：替换式中的变量即使也在 mapping 里，也不会被二次
          代换，因此 {"x": y, "y": x} 是交换，而非全部变成 x；
        - 值为 0 时，含该变量正次幂的项被消去（0 的正整数次幂为 0），
          不含该变量的项（含常数项）不受影响；
        - budget 为 None 时完全展开并返回 Poly；否则先按上界估计展开
          项数，估计不超预算才展开返回 Poly，超出预算则返回保留原式的
          Expr（精确值的未展开形式，绝不返回截断的部分和）。
        """
        unknown = set(mapping) - set(self.variables)
        if unknown:
            raise ValueError(f"代换目标不是本多项式的变量: {sorted(unknown)}")

        replacements = {}
        for name in self.variables:
            if name in mapping:
                replacements[name] = _as_poly(mapping[name])
            else:
                replacements[name] = Poly.var(name)

        expr = None
        for mono, coeff in self.terms.items():
            term = PolyExpr(Poly.constant(coeff))
            for var, exp in zip(self.variables, mono):
                if exp:
                    term = MulExpr(term, PowExpr(PolyExpr(replacements[var]), exp))
            expr = term if expr is None else AddExpr(expr, term)
        if expr is None:
            return Poly.zero()

        if budget is None:
            return expr.expand()
        if expr.term_estimate() <= budget:
            return expr.expand()
        return expr

    # ---- 显示 ----

    def __str__(self) -> str:
        if not self.terms:
            return "0"
        parts = []
        ordered = sorted(
            self.terms.items(),
            key=lambda kv: (-sum(kv[0]), tuple(-e for e in kv[0])),
        )
        for mono, coeff in ordered:
            factors = []
            for var, exp in zip(self.variables, mono):
                if exp:
                    factors.append(var if exp == 1 else f"{var}^{exp}")
            unit = "*".join(factors)
            if not unit:
                parts.append(str(coeff))
            elif coeff == 1:
                parts.append(unit)
            elif coeff == -1:
                parts.append(f"-{unit}")
            else:
                parts.append(f"{coeff}*{unit}")
        return " + ".join(parts).replace("+ -", "- ")

    __repr__ = __str__


class Expr:
    """未展开的多项式表达式树（精确值的符号化保留形式）。

    适用域：仅表示由 +、*、非负整数次幂构成的多项式表达式。
    expand 在预算内返回精确 Poly；预算不足抛 BudgetExceeded，
    不会返回截断的部分和。term_estimate 给出展开项数的上界估计。
    """

    def term_estimate(self) -> int:
        raise NotImplementedError

    def expand(self, budget: int | None = None) -> Poly:
        """展开为 Poly。

        适用域：budget 为 None 时无条件完全展开；否则先估计项数，
        超过预算抛 BudgetExceeded（异常内携带 self，可稍后重试），
        保证不会产出截断的假等式。
        """
        estimate = self.term_estimate()
        if budget is not None and estimate > budget:
            raise BudgetExceeded(estimate, budget, self)
        return self._expand()

    def _expand(self) -> Poly:
        raise NotImplementedError


class PolyExpr(Expr):
    """叶子节点：已展开的多项式。"""

    def __init__(self, poly: Poly):
        self.poly = poly

    def term_estimate(self) -> int:
        return len(self.poly.terms)

    def _expand(self) -> Poly:
        return self.poly

    def __str__(self) -> str:
        s = str(self.poly)
        return f"({s})" if len(self.poly.terms) > 1 else s


class AddExpr(Expr):
    """加法节点。项数上界：terms(l) + terms(r)。"""

    def __init__(self, left: Expr, right: Expr):
        self.left = left
        self.right = right

    def term_estimate(self) -> int:
        return _cap(self.left.term_estimate() + self.right.term_estimate())

    def _expand(self) -> Poly:
        return self.left.expand() + self.right.expand()

    def __str__(self) -> str:
        return f"{self.left} + {self.right}"


class MulExpr(Expr):
    """乘法节点。项数上界：terms(l) * terms(r)。"""

    def __init__(self, left: Expr, right: Expr):
        self.left = left
        self.right = right

    def term_estimate(self) -> int:
        return _cap(self.left.term_estimate() * self.right.term_estimate())

    def _expand(self) -> Poly:
        return self.left.expand() * self.right.expand()

    def __str__(self) -> str:
        def fmt(node):
            return f"({node})" if isinstance(node, AddExpr) else str(node)

        if isinstance(self.left, PolyExpr) and self.left.poly.terms == {
            (0,) * len(self.left.poly.variables): Fraction(1)
        }:
            return fmt(self.right)
        return f"{fmt(self.left)}*{fmt(self.right)}"


class PowExpr(Expr):
    """非负整数次幂节点。

    适用域：n 为非负整数。项数上界：t 项多项式的 n 次幂，其不同
    单项式个数不超过可重复组合数 C(t + n - 1, n)（同类项合并只会
    更少），故该估计是安全上界。0^0 按空积约定为 1。
    """

    def __init__(self, base: Expr, n: int):
        if isinstance(n, bool) or not isinstance(n, Integral) or n < 0:
            raise ValueError("仅支持非负整数次幂")
        self.base = base
        self.n = int(n)

    def term_estimate(self) -> int:
        t = self.base.term_estimate()
        if self.n == 0:
            return 1
        if t == 0:
            return 0
        return _capped_comb(t + self.n - 1, self.n)

    def _expand(self) -> Poly:
        return self.base.expand() ** self.n

    def __str__(self) -> str:
        base = str(self.base)
        if isinstance(self.base, (AddExpr, MulExpr)):
            base = f"({base})"
        return f"{base}^{self.n}"
