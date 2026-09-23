"""外存归并：排序运行段写盘 + k 路归并。

当乘积累加器超过内存额度时，把当前项按规范全序排序写入临时文件
（一个运行段），最后对所有运行段与内存余量做 k 路归并。

适用域：单项式全序取 core.monomial_sort_key（分级字典序）；系数在 Q 上
精确相加，重复单项式跨段合并，和为零的项在归并时回收。归并完成后
临时文件即被删除。
"""

from __future__ import annotations

import heapq
import os
import tempfile
from fractions import Fraction

from .core import Polynomial, monomial_sort_key


def _format_term(exp: tuple[int, ...], coeff: Fraction) -> str:
    return ",".join(map(str, exp)) + "|" + f"{coeff.numerator}/{coeff.denominator}\n"


def _parse_term(line: str):
    exp_s, frac_s = line.rstrip("\n").split("|")
    exp = tuple(int(t) for t in exp_s.split(",")) if exp_s else ()
    num, den = frac_s.split("/")
    return exp, Fraction(int(num), int(den))


def spill_run(acc: dict, spill_dir: str | None = None) -> str:
    """把 {指数: 系数} 按规范全序写入临时运行段文件，返回路径。"""
    fd, path = tempfile.mkstemp(prefix="poly_run_", suffix=".terms", dir=spill_dir)
    with os.fdopen(fd, "w", encoding="utf-8") as fh:
        for exp in sorted(acc, key=monomial_sort_key):
            fh.write(_format_term(exp, acc[exp]))
    return path


def _iter_run(path: str):
    with open(path, encoding="utf-8") as fh:
        for line in fh:
            if line.strip():
                yield _parse_term(line)


def merge_runs(run_paths: list[str], acc: dict, nvars: int,
               budget=None) -> Polynomial:
    """k 路归并所有运行段与内存余量，返回规范多项式并删除临时文件。"""
    sources = [iter(sorted(acc.items(), key=lambda kv: monomial_sort_key(kv[0])))]
    sources.extend(_iter_run(p) for p in run_paths)

    heap = []
    for idx, it in enumerate(sources):
        first = next(it, None)
        if first is not None:
            exp, c = first
            heapq.heappush(heap, (monomial_sort_key(exp), idx, exp, c))

    out: dict[tuple[int, ...], Fraction] = {}
    cur_exp = None
    cur_coeff = Fraction(0)

    def flush():
        nonlocal cur_exp, cur_coeff
        if cur_exp is not None and cur_coeff != 0:
            out[cur_exp] = cur_coeff  # 零项不写入，直接回收
        cur_exp = None
        cur_coeff = Fraction(0)

    while heap:
        _, idx, exp, c = heapq.heappop(heap)
        if cur_exp is None:
            cur_exp, cur_coeff = exp, c
        elif exp == cur_exp:
            cur_coeff += c  # 重复单项式精确合并
        else:
            flush()
            cur_exp, cur_coeff = exp, c
        nxt = next(sources[idx], None)
        if nxt is not None:
            e2, c2 = nxt
            heapq.heappush(heap, (monomial_sort_key(e2), idx, e2, c2))
    flush()

    for p in run_paths:
        os.remove(p)
    if budget is not None:
        budget.track_terms(len(out))
    return Polynomial(nvars, out)
