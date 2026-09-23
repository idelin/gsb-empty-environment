"""稀疏多元多项式：系数为有理数（fractions.Fraction），仅使用标准库。

多项式表示为 {单项式: 系数}，单项式是与变量顺序对齐的次数元组。
所有运算均为精确有理数运算；展开项数受 TermBudget 限制，超限抛出
BudgetExceeded，由上层判定为“未知”。
"""
from __future__ import annotations

from fractions import Fraction


class BudgetExceeded(Exception):
    """展开项数超过预算。"""


class TermBudget:
    """展开项数预算：任一中间多项式的项数不得超过上限。"""

    def __init__(self, max_terms: int):
        self.max_terms = max_terms

    def check(self, poly: "Poly") -> None:
        if len(poly.terms) > self.max_terms:
            raise BudgetExceeded(
                f"项数 {len(poly.terms)} 超过预算 {self.max_terms}")


class Poly:
    __slots__ = ("nvars", "terms")

    def __init__(self, nvars, terms=None):
        self.nvars = nvars
        self.terms = terms if terms is not None else {}

    @staticmethod
    def zero(nvars):
        return Poly(nvars, {})

    @staticmethod
    def constant(nvars, value):
        value = Fraction(value)
        if value == 0:
            return Poly.zero(nvars)
        return Poly(nvars, {(0,) * nvars: value})

    @staticmethod
    def variable(nvars, index):
        mono = [0] * nvars
        mono[index] = 1
        return Poly(nvars, {tuple(mono): Fraction(1)})

    def is_zero(self):
        return not self.terms

    def is_constant(self):
        return all(not any(mono) for mono in self.terms)

    def leading_monomial(self):
        return max(self.terms)

    def leading_coeff(self):
        return self.terms[self.leading_monomial()]

    def total_degree(self):
        if not self.terms:
            return 0
        return max(sum(mono) for mono in self.terms)

    def degree_in(self, k):
        if not self.terms:
            return 0
        return max(mono[k] for mono in self.terms)

    def evaluate(self, point):
        total = Fraction(0)
        for mono, coeff in self.terms.items():
            term = coeff
            for base, exp in zip(point, mono):
                if exp:
                    term *= base ** exp
            total += term
        return total

    def __eq__(self, other):
        return (isinstance(other, Poly)
                and self.nvars == other.nvars
                and self.terms == other.terms)

    def __repr__(self):
        return f"Poly({self.nvars}, {self.terms!r})"


def _combine(a, b, sign, budget):
    terms = dict(a.terms)
    for mono, coeff in b.terms.items():
        new = terms.get(mono, Fraction(0)) + sign * coeff
        if new:
            terms[mono] = new
        else:
            terms.pop(mono, None)
    out = Poly(a.nvars, terms)
    if budget is not None:
        budget.check(out)
    return out


def add(a, b, budget=None):
    return _combine(a, b, 1, budget)


def sub(a, b, budget=None):
    return _combine(a, b, -1, budget)


def neg(a):
    return Poly(a.nvars, {m: -c for m, c in a.terms.items()})


def scale(a, factor):
    factor = Fraction(factor)
    if factor == 0:
        return Poly.zero(a.nvars)
    return Poly(a.nvars, {m: c * factor for m, c in a.terms.items()})


def mul(a, b, budget=None):
    if a.is_zero() or b.is_zero():
        return Poly.zero(a.nvars)
    terms = {}
    for ma, ca in a.terms.items():
        for mb, cb in b.terms.items():
            mono = tuple(x + y for x, y in zip(ma, mb))
            terms[mono] = terms.get(mono, Fraction(0)) + ca * cb
    terms = {m: c for m, c in terms.items() if c}
    out = Poly(a.nvars, terms)
    if budget is not None:
        budget.check(out)
    return out


def poly_pow(base, exp, budget=None):
    """非负整数次幂，平方连乘。"""
    result = Poly.constant(base.nvars, 1)
    b = base
    e = exp
    while e > 0:
        if e & 1:
            result = mul(result, b, budget)
        e >>= 1
        if e:
            b = mul(b, b, budget)
    return result


def derivative(f, k):
    terms = {}
    for mono, coeff in f.terms.items():
        e = mono[k]
        if e:
            m = list(mono)
            m[k] -= 1
            terms[tuple(m)] = coeff * e
    return Poly(f.nvars, terms)


def div_exact(f, g, budget=None):
    """精确整除（仅在前置条件保证整除时调用），否则抛 ValueError。"""
    if g.is_zero():
        raise ZeroDivisionError("多项式除以零")
    quotient = Poly.zero(f.nvars)
    remainder = Poly(f.nvars, dict(f.terms))
    g_mono = g.leading_monomial()
    g_coeff = g.terms[g_mono]
    while not remainder.is_zero():
        r_mono = remainder.leading_monomial()
        quot_mono = tuple(a - b for a, b in zip(r_mono, g_mono))
        if any(e < 0 for e in quot_mono):
            raise ValueError("非精确整除")
        t = Poly(f.nvars, {quot_mono: remainder.terms[r_mono] / g_coeff})
        quotient = add(quotient, t, budget)
        remainder = sub(remainder, mul(t, g, budget), budget)
    return quotient


def monic(f):
    """首项系数（字典序首项）化为 1，使比较与单位因子无关。"""
    if f.is_zero():
        return f
    return scale(f, 1 / f.leading_coeff())


def _coeffs_in(f, k):
    """把 f 看作变量 k 的一元多项式，返回 {次数: 系数多项式}。"""
    coeffs = {}
    for mono, coeff in f.terms.items():
        e = mono[k]
        m = list(mono)
        m[k] = 0
        m = tuple(m)
        bucket = coeffs.setdefault(e, {})
        bucket[m] = coeff
    return {e: Poly(f.nvars, terms) for e, terms in coeffs.items()}


def _leading_coeff_in(f, k):
    d = f.degree_in(k)
    terms = {}
    for mono, coeff in f.terms.items():
        if mono[k] == d:
            m = list(mono)
            m[k] = 0
            terms[tuple(m)] = coeff
    return Poly(f.nvars, terms)


def _shift(f, k, amount):
    """乘以 x_k^amount。"""
    terms = {}
    for mono, coeff in f.terms.items():
        m = list(mono)
        m[k] += amount
        terms[tuple(m)] = coeff
    return Poly(f.nvars, terms)


def _content_in(f, k, rest, budget):
    """f 关于变量 k 的内容（各系数多项式的最大公因式）。"""
    acc = Poly.zero(f.nvars)
    for coeff_poly in _coeffs_in(f, k).values():
        acc = _gcd_vars(acc, coeff_poly, rest, budget)
    return acc


def _primitive_part(f, k, rest, budget):
    if f.is_zero():
        return f
    return div_exact(f, _content_in(f, k, rest, budget), budget)


def _pseudo_rem(a, b, k, budget):
    """关于变量 k 的伪余式：lc(b)^delta * a 除以 b 的余式。"""
    db = b.degree_in(k)
    lb = _leading_coeff_in(b, k)
    r = a
    while not r.is_zero() and r.degree_in(k) >= db:
        dr = r.degree_in(k)
        lr = _leading_coeff_in(r, k)
        t = mul(_shift(lr, k, dr - db), b, budget)
        r = sub(mul(lb, r, budget), t, budget)
    return r


def _gcd_primitive(a, b, k, rest, budget):
    """两个关于 k 的本原多项式的最大公因式（本原 PRS）。"""
    if a.degree_in(k) < b.degree_in(k):
        a, b = b, a
    while not b.is_zero() and b.degree_in(k) > 0:
        r = _pseudo_rem(a, b, k, budget)
        a = b
        b = _primitive_part(r, k, rest, budget) if not r.is_zero() else Poly.zero(a.nvars)
    if b.is_zero():
        return _primitive_part(a, k, rest, budget)
    # b 不含变量 k 且 a 本原：公因式为单位
    return Poly.constant(a.nvars, 1)


def _gcd_vars(f, g, var_indices, budget):
    if f.is_zero():
        return monic(g)
    if g.is_zero():
        return monic(f)
    if not var_indices:
        return Poly.constant(f.nvars, 1)
    k = var_indices[0]
    rest = var_indices[1:]
    cf = _content_in(f, k, rest, budget)
    cg = _content_in(g, k, rest, budget)
    content_gcd = _gcd_vars(cf, cg, rest, budget)
    pf = div_exact(f, content_gcd, budget)
    pg = div_exact(g, content_gcd, budget)
    h = _gcd_primitive(pf, pg, k, rest, budget)
    return monic(mul(content_gcd, h, budget))


def gcd(f, g, budget=None):
    """多元多项式最大公因式（首一化，精确有理数运算）。"""
    assert f.nvars == g.nvars
    return _gcd_vars(f, g, tuple(range(f.nvars)), budget)


def squarefree_part(f, budget=None):
    """无平方部分：保留每个不可约因子各一份（首一化）。

    迭代执行 r <- r / gcd(r, r 的所有偏导)，直到公因式为常数。
    适用域：特征 0 的有理数域。
    """
    if f.is_zero():
        return f
    r = f
    all_vars = tuple(range(f.nvars))
    while True:
        g = r
        for k in range(f.nvars):
            g = _gcd_vars(g, derivative(r, k), all_vars, budget)
        if g.is_constant():
            return monic(r)
        r = div_exact(r, g, budget)


def same_up_to_scalar(f, g):
    """两多项式是否相差非零有理数倍。"""
    if f.is_zero() or g.is_zero():
        return f.is_zero() and g.is_zero()
    return monic(f).terms == monic(g).terms


def poly_to_str(poly, var_names):
    if not poly.terms:
        return "0"
    parts = []
    for mono in sorted(poly.terms, reverse=True):
        coeff = poly.terms[mono]
        factors = []
        for name, exp in zip(var_names, mono):
            if exp == 1:
                factors.append(name)
            elif exp > 1:
                factors.append(f"{name}^{exp}")
        monomial = "*".join(factors)
        if monomial:
            if coeff == 1:
                parts.append(monomial)
            elif coeff == -1:
                parts.append("-" + monomial)
            else:
                parts.append(f"{coeff}*{monomial}")
        else:
            parts.append(str(coeff))
    text = parts[0]
    for part in parts[1:]:
        if part.startswith("-"):
            text += " - " + part[1:]
        else:
            text += " + " + part
    return text
