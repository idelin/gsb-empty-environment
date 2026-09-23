"""独立稠密小域参考实现：GF(p) 上的稠密多维数组多项式。

用途：作为稀疏有理数实现的独立交叉检验参照——
* 表示不同（稠密数组 vs 稀疏字典）；
* 系数域不同（GF(p) vs Q）；
* 不共享任何运算代码。
稀疏结果 mod p 投影后必须与稠密参考逐项一致；偏导交换关系
d/dx_i d/dx_j == d/dx_j d/dx_i 在两侧都应成立。
"""
from __future__ import annotations

from poly import Polynomial

__all__ = ["DenseModP", "sparse_mod_p"]


class DenseModP:
    """GF(p) 上的稠密多项式：data 为混合进制展开的平坦数组。

    指数向量 (e_0, ..., e_{n-1}) 映射到下标
    ``sum(e_i * (max_deg+1)**i)``；nvars=0 时数组长度为 1（常数）。
    """

    __slots__ = ("nvars", "max_deg", "p", "data")

    def __init__(self, nvars: int, max_deg: int, p: int, data=None):
        if nvars < 0 or max_deg < 0:
            raise ValueError("nvars/max_deg 必须非负")
        self.nvars = nvars
        self.max_deg = max_deg
        self.p = p
        size = (max_deg + 1) ** nvars
        if data is None:
            self.data = [0] * size
        else:
            if len(data) != size:
                raise ValueError("稠密数组长度不符")
            self.data = [x % p for x in data]

    # ------------------------------------------------------------ 下标
    def _index(self, exp: tuple[int, ...]) -> int:
        idx = 0
        for i, e in enumerate(exp):
            if e > self.max_deg:
                raise ValueError(f"次数越界: {exp}（max_deg={self.max_deg}）")
            idx += e * (self.max_deg + 1) ** i
        return idx

    def _exp_of(self, idx: int) -> tuple[int, ...]:
        exp = []
        base = self.max_deg + 1
        for _ in range(self.nvars):
            exp.append(idx % base)
            idx //= base
        return tuple(exp)

    # ------------------------------------------------------------ 转换
    @classmethod
    def from_sparse(cls, poly: Polynomial, p: int, max_deg: int) -> "DenseModP":
        """把稀疏有理多项式投影到 GF(p)（要求分母不被 p 整除）。"""
        dense = cls(poly.nvars, max_deg, p)
        for exp, coef in poly._terms.items():
            dense.data[dense._index(exp)] = _frac_mod_p(coef, p)
        return dense

    def to_dict(self) -> dict:
        return {
            self._exp_of(i): c for i, c in enumerate(self.data) if c % self.p
        }

    # ------------------------------------------------------------ 运算
    def __add__(self, other: "DenseModP") -> "DenseModP":
        """稠密加法。适用域：任意域。"""
        self._check(other)
        p = self.p
        return DenseModP(
            self.nvars, self.max_deg, p,
            [(a + b) % p for a, b in zip(self.data, other.data)],
        )

    def __eq__(self, other) -> bool:
        return (
            isinstance(other, DenseModP)
            and self.nvars == other.nvars
            and self.p == other.p
            and self.data == other.data
        )

    def __mul__(self, other: "DenseModP") -> "DenseModP":
        """稠密乘法（卷积，次数超限的乘积项拒绝）。适用域：任意交换环。"""
        self._check(other)
        p, nvars, max_deg = self.p, self.nvars, self.max_deg
        out = [0] * len(self.data)
        for ia, ca in enumerate(self.data):
            if ca == 0:
                continue
            ea = self._exp_of(ia)
            for ib, cb in enumerate(other.data):
                if cb == 0:
                    continue
                eb = self._exp_of(ib)
                exp = tuple(a + b for a, b in zip(ea, eb))
                if any(e > max_deg for e in exp):
                    raise ValueError("乘积次数超出稠密参考的 max_deg")
                out[self._index(exp)] = (out[self._index(exp)] + ca * cb) % p
        return DenseModP(nvars, max_deg, p, out)

    def deriv(self, var: int) -> "DenseModP":
        """稠密形式偏导。适用域：任意域（特征 p 时 e_i 按 mod p 缩放）。"""
        if not 0 <= var < self.nvars:
            raise ValueError(f"求导变量下标越界: {var}")
        p = self.p
        out = [0] * len(self.data)
        for idx, coef in enumerate(self.data):
            if coef == 0:
                continue
            exp = list(self._exp_of(idx))
            k = exp[var]
            if k == 0:
                continue
            exp[var] = k - 1
            out[self._index(tuple(exp))] = (coef * (k % p)) % p
        return DenseModP(self.nvars, self.max_deg, p, out)

    def _check(self, other: "DenseModP") -> None:
        if (self.nvars, self.max_deg, self.p) != (other.nvars, other.max_deg, other.p):
            raise ValueError("稠密参考参数不一致")


def _frac_mod_p(frac, p: int) -> int:
    den = frac.denominator % p
    if den == 0:
        raise ValueError(f"分母被 p={p} 整除，无法投影: {frac}")
    return (frac.numerator % p) * pow(den, -1, p) % p


def sparse_mod_p(poly: Polynomial, p: int) -> dict:
    """稀疏多项式 mod p 投影为 {指数向量: GF(p) 元素}（去零）。"""
    out = {}
    for exp, coef in poly._terms.items():
        v = _frac_mod_p(coef, p)
        if v:
            out[exp] = v
    return out
