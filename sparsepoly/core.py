"""稀疏多变量多项式核心：指数向量 -> 有理系数的字典存储。

规范形式：系数为零的项一律回收（不存于字典），因此 ``x - x`` 得到空字典，
即零多项式。输出顺序由 ``monomial_sort_key`` 给出确定性全序（分级字典序）。

适用域说明：
- 系数域为有理数域 Q（特征 0 的域），全部运算精确、不调用符号代数库。
- 单项式指数为非负整数；负指数（Laurent 单项式）一律拒绝。
- 乘法依赖系数环的交换律；本库不处理非交换情形。
- 零变量（nvars=0）时退化为常数环 Q，常数多项式有定义。
"""

from __future__ import annotations

from fractions import Fraction


def monomial_sort_key(exp: tuple[int, ...]) -> tuple[int, tuple[int, ...]]:
    """单项式全序：先总次数，再按指数向量字典序。用于规范输出与外存归并。"""
    return (sum(exp), exp)


class Polynomial:
    """稀疏多变量多项式。``terms`` 为 {指数元组: Fraction}，不含零系数项。"""

    __slots__ = ("nvars", "_terms")

    def __init__(self, nvars: int, terms: dict | None = None):
        if not isinstance(nvars, int) or nvars < 0:
            raise ValueError(f"nvars 必须为非负整数，得到 {nvars!r}")
        self.nvars = nvars
        clean: dict[tuple[int, ...], Fraction] = {}
        for exp, coeff in (terms or {}).items():
            exp = tuple(exp)
            if len(exp) != nvars:
                raise ValueError(
                    f"指数向量长度 {len(exp)} 与变量数 {nvars} 不一致: {exp}"
                )
            for e in exp:
                if not isinstance(e, int) or e < 0:
                    raise ValueError(f"负指数或非整数指数被拒绝（仅支持多项式）: {exp}")
            c = Fraction(coeff)
            if c != 0:
                clean[exp] = c
        self._terms = clean

    # ---- 构造器 ----

    @classmethod
    def zero(cls, nvars: int) -> "Polynomial":
        return cls(nvars)

    @classmethod
    def constant(cls, value, nvars: int = 0) -> "Polynomial":
        """常数多项式；nvars=0 时为零变量常数，依然有定义。"""
        return cls(nvars, {(0,) * nvars: Fraction(value)})

    @classmethod
    def variable(cls, index: int, nvars: int) -> "Polynomial":
        if not 0 <= index < nvars:
            raise ValueError(f"变量下标 {index} 超出 [0, {nvars})")
        exp = [0] * nvars
        exp[index] = 1
        return cls(nvars, {tuple(exp): Fraction(1)})

    # ---- 基本访问 ----

    @property
    def terms(self) -> dict[tuple[int, ...], Fraction]:
        return dict(self._terms)

    def is_zero(self) -> bool:
        return not self._terms

    @property
    def num_terms(self) -> int:
        return len(self._terms)

    def sorted_terms(self) -> list[tuple[tuple[int, ...], Fraction]]:
        """按规范全序（次数降序、字典序降序）返回 [(指数, 系数), ...]。"""
        return sorted(self._terms.items(), key=lambda kv: monomial_sort_key(kv[0]),
                      reverse=True)

    def total_degree(self) -> int:
        return max((sum(e) for e in self._terms), default=0)

    # ---- 运算（每步注明适用域） ----

    def _check_compat(self, other: "Polynomial") -> None:
        if not isinstance(other, Polynomial):
            raise TypeError(f"不能与 {type(other).__name__} 做多项式运算")
        if self.nvars != other.nvars:
            raise ValueError(f"变量数不一致: {self.nvars} vs {other.nvars}")

    def __add__(self, other: "Polynomial") -> "Polynomial":
        """加法：同指数项精确合并，和为零的项回收。适用域：任意交换环（此处 Q）。"""
        self._check_compat(other)
        out = dict(self._terms)
        for exp, c in other._terms.items():
            old = out.get(exp)
            if old is None:
                out[exp] = c
            else:
                v = old + c
                if v == 0:
                    del out[exp]  # 零项回收
                else:
                    out[exp] = v
        return Polynomial(self.nvars, out)

    def __neg__(self) -> "Polynomial":
        return Polynomial(self.nvars, {e: -c for e, c in self._terms.items()})

    def __sub__(self, other: "Polynomial") -> "Polynomial":
        """减法 = 加相反元；x - x 经零项回收得到零多项式。适用域：任意环。"""
        return self + (-other)

    def __mul__(self, other: "Polynomial") -> "Polynomial":
        """教科书式乘法：交叉项按指数和合并。适用域：交换环（此处 Q）。"""
        self._check_compat(other)
        acc: dict[tuple[int, ...], Fraction] = {}
        for e1, c1 in self._terms.items():
            for e2, c2 in other._terms.items():
                exp = tuple(a + b for a, b in zip(e1, e2))
                c = c1 * c2
                old = acc.get(exp)
                if old is None:
                    acc[exp] = c
                else:
                    v = old + c
                    if v == 0:
                        del acc[exp]
                    else:
                        acc[exp] = v
        return Polynomial(self.nvars, acc)

    def partial(self, index: int) -> "Polynomial":
        """对第 index 个变量求偏导：d(c*x^e)/dx_i = c*e_i*x^(e_i-1)。

        适用域：特征 0 的环（此处 Q）。e_i=0 的项导数为 0 被回收。
        注意：在特征 p>0 的域上该法则对 e_i 为 p 的倍数的项给出 0，
        本库不在有限特征下工作（有限特征参考检验见 tests 中的稠密实现）。
        """
        if not 0 <= index < self.nvars:
            raise IndexError(f"偏导变量下标 {index} 超出 [0, {self.nvars})")
        out: dict[tuple[int, ...], Fraction] = {}
        for exp, c in self._terms.items():
            e = exp[index]
            if e:
                new = list(exp)
                new[index] = e - 1
                out[tuple(new)] = c * e
        return Polynomial(self.nvars, out)

    # ---- 比较与展示 ----

    def __eq__(self, other) -> bool:
        return (isinstance(other, Polynomial)
                and self.nvars == other.nvars
                and self._terms == other._terms)

    def to_string(self, names: list[str] | None = None) -> str:
        if not self._terms:
            return "0"
        names = names or [f"x{i}" for i in range(self.nvars)]
        parts = []
        for exp, c in self.sorted_terms():
            mono = "*".join(
                n if e == 1 else f"{n}^{e}" for n, e in zip(names, exp) if e
            )
            if not mono:
                parts.append(str(c))
            elif c == 1:
                parts.append(mono)
            else:
                parts.append(f"{c}*{mono}")
        return " + ".join(parts).replace("+ -", "- ")

    def __repr__(self) -> str:
        return f"Polynomial(nvars={self.nvars}, {self.to_string()})"
