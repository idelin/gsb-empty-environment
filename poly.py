"""多变量稀疏多项式：有理数精确系数，按单项式指数向量存储。

设计要点
--------
* 存储：``{指数向量 tuple[int, ...]: fractions.Fraction}``，零项随时回收。
* 序：  单项式按"分级字典序"（先总次数，再字典序）比较；canonical 输出
  按次数降序排列，保证同一数学对象有唯一字符串表示。
* 精确性：仅使用标准库 ``fractions.Fraction``，不调用任何符号代数库。
* 每个公开运算的 docstring 首行标注该变换的适用域。
"""
from __future__ import annotations

import heapq
import os
import tempfile
from fractions import Fraction
from itertools import groupby
from operator import itemgetter

__all__ = [
    "Polynomial",
    "TermLimitExceeded",
    "multiply",
    "monomial_key",
]

DEFAULT_MEM_BUDGET = 1 << 16  # 内存中允许暂存的乘积项数上限（超过则走外存归并）


class TermLimitExceeded(RuntimeError):
    """项数膨胀超过硬上限时抛出。"""


def monomial_key(exp: tuple[int, ...]) -> tuple[int, tuple[int, ...]]:
    """分级字典序（graded lex）升序键：先总次数，再指数向量字典序。"""
    return (sum(exp), exp)


def _validate_exponents(exp: tuple[int, ...]) -> None:
    for e in exp:
        if not isinstance(e, int) or isinstance(e, bool) or e < 0:
            raise ValueError(f"拒绝负指数或非整数指数: {exp!r}")


def _check_same_nvars(a: "Polynomial", b: "Polynomial") -> None:
    if a.nvars != b.nvars:
        raise ValueError(f"变量个数不一致: {a.nvars} 与 {b.nvars}")


class Polynomial:
    """n 元稀疏多项式，系数为 Q（Fraction），精确运算。

    构造接受 映射 {指数向量: 系数} 或 [(指数向量, 系数), ...] 序列；
    重复单项式在构造时即精确合并，系数为零的项立即回收。
    零变量（nvars=0）时常数多项式有定义，指数向量为空元组 ()。
    """

    __slots__ = ("nvars", "_terms")

    def __init__(self, nvars: int, terms=None):
        if not isinstance(nvars, int) or nvars < 0:
            raise ValueError(f"nvars 必须是非负整数: {nvars!r}")
        self.nvars = nvars
        clean: dict[tuple[int, ...], Fraction] = {}
        if terms:
            items = terms.items() if hasattr(terms, "items") else terms
            for exp, coef in items:
                exp = tuple(exp)
                if len(exp) != nvars:
                    raise ValueError(
                        f"指数向量长度 {len(exp)} 与 nvars={nvars} 不符: {exp!r}"
                    )
                _validate_exponents(exp)
                c = Fraction(coef)
                if not c:
                    continue
                merged = clean.get(exp)
                merged = c if merged is None else merged + c
                if merged:
                    clean[exp] = merged
                else:
                    clean.pop(exp, None)  # 抵消为零，回收
        self._terms = clean

    # ------------------------------------------------------------- 构造器
    @classmethod
    def zero(cls, nvars: int) -> "Polynomial":
        """零多项式。适用域：任意环。"""
        return cls(nvars)

    @classmethod
    def constant(cls, value, nvars: int) -> "Polynomial":
        """常数多项式（nvars=0 时为零变量常数）。适用域：任意含单位元环。"""
        return cls(nvars, {(0,) * nvars: Fraction(value)})

    @classmethod
    def variable(cls, index: int, nvars: int) -> "Polynomial":
        """第 index 个不定元 x_index。适用域：任意含单位元环。"""
        if not 0 <= index < nvars:
            raise ValueError(f"变量下标越界: {index}（nvars={nvars}）")
        exp = [0] * nvars
        exp[index] = 1
        return cls(nvars, {tuple(exp): Fraction(1)})

    # ------------------------------------------------------------- 基本性质
    @property
    def term_count(self) -> int:
        return len(self._terms)

    def is_zero(self) -> bool:
        return not self._terms

    def sorted_terms(self, descending: bool = True):
        """canonical 顺序的 [(指数向量, 系数)]：次数降序、同级字典序升序。"""
        items = sorted(self._terms.items(), key=lambda kv: monomial_key(kv[0]))
        if descending:
            items.reverse()
        return items

    def __eq__(self, other) -> bool:
        return (
            isinstance(other, Polynomial)
            and self.nvars == other.nvars
            and self._terms == other._terms
        )

    __hash__ = None  # 多项式不作为字典键使用

    # ------------------------------------------------------------- 显示
    def _format_monomial(self, exp: tuple[int, ...]) -> str:
        parts = []
        for i, e in enumerate(exp):
            if e == 0:
                continue
            parts.append(f"x{i}" if e == 1 else f"x{i}^{e}")
        return "*".join(parts) if parts else "1"

    def to_string(self) -> str:
        """canonical 字符串：次数降序、系数精确、零多项式为 "0"。"""
        if not self._terms:
            return "0"
        chunks = []
        for exp, coef in self.sorted_terms():
            mono = self._format_monomial(exp)
            chunks.append(f"({coef})*{mono}" if mono != "1" else f"({coef})")
        return " + ".join(chunks)

    def __repr__(self) -> str:
        return f"Polynomial(nvars={self.nvars}, {self.to_string()})"

    # ------------------------------------------------------------- 加法
    def __add__(self, other: "Polynomial") -> "Polynomial":
        """加法（合并同类项，零项回收）。适用域：任意交换环。"""
        _check_same_nvars(self, other)
        acc = dict(self._terms)
        for exp, coef in other._terms.items():
            merged = acc.get(exp)
            merged = coef if merged is None else merged + coef
            if merged:
                acc[exp] = merged
            else:
                acc.pop(exp, None)
        return Polynomial(self.nvars, acc)

    def __neg__(self) -> "Polynomial":
        """加法逆元。适用域：任意环。"""
        return Polynomial(self.nvars, {e: -c for e, c in self._terms.items()})

    def __sub__(self, other: "Polynomial") -> "Polynomial":
        """减法（x - x 精确得零多项式）。适用域：任意环。"""
        return self + (-other)

    # ------------------------------------------------------------- 乘法
    def __mul__(self, other: "Polynomial") -> "Polynomial":
        """乘法（默认内存额度，超出自动外存归并）。适用域：任意交换环。"""
        return multiply(self, other)

    # ------------------------------------------------------------- 偏导
    def deriv(self, var: int) -> "Polynomial":
        """对第 var 个变量的形式偏导。

        适用域：特征 0 的整环（此处为 Q）；规则 d(x^e)/dx_i = e_i * x^(e - u_i)，
        e_i = 0 的项映为零并被回收。零变量多项式上任何求导下标均越界。
        """
        if not 0 <= var < self.nvars:
            raise ValueError(f"求导变量下标越界: {var}（nvars={self.nvars}）")
        out: dict[tuple[int, ...], Fraction] = {}
        for exp, coef in self._terms.items():
            k = exp[var]
            if k == 0:
                continue
            new_exp = list(exp)
            new_exp[var] = k - 1
            out[tuple(new_exp)] = coef * k
        return Polynomial(self.nvars, out)


# ================================================================= 乘法
def _mul_inplace(p: Polynomial, q: Polynomial) -> Polynomial:
    """纯内存稀疏乘法。适用域：任意交换环。"""
    acc: dict[tuple[int, ...], Fraction] = {}
    for pe, pc in p._terms.items():
        for qe, qc in q._terms.items():
            exp = tuple(a + b for a, b in zip(pe, qe))
            merged = acc.get(exp)
            merged = pc * qc if merged is None else merged + pc * qc
            if merged:
                acc[exp] = merged
            else:
                acc.pop(exp, None)
    return Polynomial(p.nvars, acc)


def _run_path(dirpath: str, index: int) -> str:
    return os.path.join(dirpath, f"run-{index:06d}.run")


def _write_run(path: str, buffer: list) -> None:
    buffer.sort(key=lambda kv: monomial_key(kv[0]))
    with open(path, "w", encoding="utf-8") as fh:
        for exp, coef in buffer:
            fh.write(
                f"{' '.join(map(str, exp))};{coef.numerator};{coef.denominator}\n"
            )


def _iter_run(path: str, nvars: int):
    with open(path, "r", encoding="utf-8") as fh:
        for line in fh:
            exp_s, num_s, den_s = line.rstrip("\n").split(";")
            exp = tuple(int(t) for t in exp_s.split()) if exp_s else ()
            if len(exp) != nvars:
                raise ValueError(f"外存归并段损坏: {path}")
            yield exp, Fraction(int(num_s), int(den_s))


def _mul_external(p: Polynomial, q: Polynomial, mem_budget: int,
                  spill_dir: str | None = None) -> Polynomial:
    """内存额度受限的外存归并乘法。

    适用域：任意交换环。流程：乘积项流式生成 -> 缓冲区满即排序落盘为
    有序归并段 -> k 路归并（heapq.merge）-> 重复单项式精确相加、零项回收。
    """
    if mem_budget < 1:
        raise ValueError("mem_budget 必须 >= 1")
    q_terms = sorted(q._terms.items(), key=lambda kv: monomial_key(kv[0]))
    p_terms = sorted(p._terms.items(), key=lambda kv: monomial_key(kv[0]))

    ctx = tempfile.TemporaryDirectory(prefix="polymul-") if spill_dir is None else None
    dirpath = spill_dir if spill_dir is not None else ctx.name
    runs: list[str] = []
    buffer: list = []
    try:
        for pe, pc in p_terms:
            for qe, qc in q_terms:
                buffer.append((tuple(a + b for a, b in zip(pe, qe)), pc * qc))
                if len(buffer) >= mem_budget:
                    path = _run_path(dirpath, len(runs))
                    _write_run(path, buffer)
                    runs.append(path)
                    buffer.clear()
        if buffer or not runs:
            path = _run_path(dirpath, len(runs))
            _write_run(path, buffer)
            runs.append(path)
            buffer.clear()

        streams = [_iter_run(path, p.nvars) for path in runs]
        merged = heapq.merge(*streams, key=lambda kv: monomial_key(kv[0]))
        acc: dict[tuple[int, ...], Fraction] = {}
        for exp, group in groupby(merged, key=itemgetter(0)):
            total = Fraction(0)
            for _, coef in group:
                total += coef
            if total:
                acc[exp] = total
        return Polynomial(p.nvars, acc)
    finally:
        if ctx is not None:
            ctx.cleanup()
        else:
            for path in runs:
                if os.path.exists(path):
                    os.remove(path)


def multiply(p: Polynomial, q: Polynomial,
             mem_budget: int = DEFAULT_MEM_BUDGET,
             spill_dir: str | None = None) -> Polynomial:
    """稀疏乘法：乘积项数不超过 mem_budget 走内存，否则外存归并。

    适用域：任意交换环。两条路径结果均为同一 canonical 多项式。
    """
    _check_same_nvars(p, q)
    if p.is_zero() or q.is_zero():
        return Polynomial.zero(p.nvars)
    if len(p._terms) * len(q._terms) <= mem_budget:
        return _mul_inplace(p, q)
    return _mul_external(p, q, mem_budget, spill_dir)
