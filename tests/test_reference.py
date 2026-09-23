"""独立参考检验：稠密小域 GF(p) 实现对照稀疏 Q 实现。

DensePoly 是刻意独立的参考实现：稠密混合进制平坦数组、GF(101) 算术、
不共享 sparsepoly 的任何代码。检验内容：
- 加/乘/偏导在 mod p 投影下与稀疏实现一致；
- 偏导交换关系 d/dxi d/dxj = d/dxj d/dxi（Q 上与 GF(p) 上分别验证）；
- Leibniz 法则 (fg)' = f'g + fg'。
"""

import random
import unittest
from fractions import Fraction

from sparsepoly import Polynomial

PRIME = 101  # 小域特征；次数远低于 p，偏导不因特征而退化


def unflatten(idx, bounds):
    exp = [0] * len(bounds)
    for k in range(len(bounds) - 1, -1, -1):
        exp[k] = idx % bounds[k]
        idx //= bounds[k]
    return tuple(exp)


class DensePoly:
    """稠密参考实现：GF(p) 上的平坦系数数组。"""

    def __init__(self, bounds, coeffs=None):
        self.bounds = tuple(bounds)
        size = 1
        for b in self.bounds:
            size *= b
        self.coeffs = [0] * size if coeffs is None else list(coeffs)

    def flat(self, exp):
        idx = 0
        for e, b in zip(exp, self.bounds):
            idx = idx * b + e
        return idx

    @classmethod
    def from_sparse(cls, poly, bounds):
        d = cls(bounds)
        for exp, c in poly.terms.items():
            num = c.numerator % PRIME
            den = c.denominator % PRIME
            d.coeffs[d.flat(exp)] = num * pow(den, -1, PRIME) % PRIME
        return d

    def add(self, other):
        return DensePoly(self.bounds,
                         [(a + b) % PRIME
                          for a, b in zip(self.coeffs, other.coeffs)])

    def mul(self, other, out_bounds):
        out = DensePoly(out_bounds)
        for i1, c1 in enumerate(self.coeffs):
            if not c1:
                continue
            e1 = unflatten(i1, self.bounds)
            for i2, c2 in enumerate(other.coeffs):
                if not c2:
                    continue
                e2 = unflatten(i2, other.bounds)
                e = tuple(a + b for a, b in zip(e1, e2))
                slot = out.flat(e)
                out.coeffs[slot] = (out.coeffs[slot] + c1 * c2) % PRIME
        return out

    def deriv(self, var):
        out = DensePoly(self.bounds)
        for idx, c in enumerate(self.coeffs):
            if not c:
                continue
            e = list(unflatten(idx, self.bounds))
            if e[var]:
                k = e[var]
                e[var] = k - 1
                slot = out.flat(tuple(e))
                out.coeffs[slot] = (out.coeffs[slot] + c * k) % PRIME
        return out


def random_sparse(rng, nvars, max_terms=6, max_deg=3):
    terms = {}
    for _ in range(rng.randint(1, max_terms)):
        exp = tuple(rng.randint(0, max_deg) for _ in range(nvars))
        terms[exp] = Fraction(rng.randint(-5, 5) or 1, rng.randint(1, 5))
    return Polynomial(nvars, terms)


def bounds_of(poly):
    if poly.is_zero():
        return (1,) * poly.nvars
    return tuple(max(e[i] for e in poly.terms) + 1 for i in range(poly.nvars))


def max_bounds(b1, b2):
    return tuple(max(a, b) for a, b in zip(b1, b2))


class TestDenseReference(unittest.TestCase):
    def setUp(self):
        self.rng = random.Random(20260923)

    def test_add_mul_mod_p(self):
        for _ in range(30):
            nvars = self.rng.randint(1, 3)
            f = random_sparse(self.rng, nvars)
            g = random_sparse(self.rng, nvars)
            b = max_bounds(bounds_of(f), bounds_of(g))
            df = DensePoly.from_sparse(f, b)
            dg = DensePoly.from_sparse(g, b)
            # 加法对照
            self.assertEqual(
                DensePoly.from_sparse(f + g, b).coeffs, df.add(dg).coeffs)
            # 乘法对照（输出界为次数和）
            out_b = tuple(x + y - 1 for x, y in zip(bounds_of(f), bounds_of(g)))
            got = DensePoly.from_sparse(f * g, out_b).coeffs
            self.assertEqual(got, df.mul(dg, out_b).coeffs)

    def test_derivative_matches_dense(self):
        for _ in range(30):
            nvars = self.rng.randint(1, 3)
            f = random_sparse(self.rng, nvars)
            b = bounds_of(f)
            df = DensePoly.from_sparse(f, b)
            for v in range(nvars):
                got = DensePoly.from_sparse(f.partial(v), b).coeffs
                self.assertEqual(got, df.deriv(v).coeffs)

    def test_derivative_commutes_sparse_and_dense(self):
        for _ in range(30):
            nvars = self.rng.randint(2, 3)
            f = random_sparse(self.rng, nvars)
            i, j = self.rng.randrange(nvars), self.rng.randrange(nvars)
            # Q 上精确验证交换关系
            self.assertEqual(f.partial(i).partial(j), f.partial(j).partial(i))
            # GF(p) 稠密参考同步验证
            b = bounds_of(f)
            df = DensePoly.from_sparse(f, b)
            self.assertEqual(df.deriv(i).deriv(j).coeffs,
                             df.deriv(j).deriv(i).coeffs)

    def test_leibniz_rule(self):
        for _ in range(20):
            nvars = self.rng.randint(1, 3)
            f = random_sparse(self.rng, nvars)
            g = random_sparse(self.rng, nvars)
            for v in range(nvars):
                lhs = (f * g).partial(v)
                rhs = f.partial(v) * g + f * g.partial(v)
                self.assertEqual(lhs, rhs)


if __name__ == "__main__":
    unittest.main()
