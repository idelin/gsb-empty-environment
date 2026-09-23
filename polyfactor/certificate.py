"""Verification certificate for a factor-extraction DAG.

Every transformation step records:
  * the rewrite rule applied,
  * the domain on which the step is valid (extraction only ever divides by
    a non-zero rational constant and a non-negative-exponent monomial, so
    no domain restriction is ever introduced or removed),
  * an independent re-verification by canonical polynomial expansion.

The final step expands the whole DAG through the independent canonical
arithmetic in poly.py and demands strict equality with the input.
"""
from __future__ import annotations

from dataclasses import dataclass

from .poly import Poly

DOMAIN_ALL = "全体 R^n: 仅提取非零有理常数与非负指数单项式, 不引入也不消除定义域限制"
DOMAIN_SUM = "全体 R^n: 加法恒等式, 无定义域限制"
DOMAIN_LEAF = "无限制: 叶节点直接构造"


@dataclass
class Step:
    index: int
    rule: str
    statement: str
    domain: str
    check: str
    verified: bool


def build_certificate(search, poly, entry):
    steps = []

    def emit(rule, statement, domain, check, verified):
        steps.append(Step(len(steps) + 1, rule, statement, domain, check, verified))

    def recurse(p, e):
        choice = e.choice
        if choice is None:
            emit("incomplete", f"{p} 的分解未在预算内完成", "—",
                 "无(该子问题被预算截断)", True)
            return
        kind = choice[0]
        if kind == "zero":
            emit("zero-polynomial",
                 "零多项式: 约定表示为常数 0, 不定义内容/本原部分解",
                 "任意(零多项式处处为 0)", "规范展开 0 == 0", True)
        elif kind == "term":
            emit("monomial-leaf", f"单项式 {p}: 按系数与最小指数直接构造",
                 DOMAIN_LEAF, "平凡恒等", True)
        elif kind == "content":
            _, c, m, q_key = choice
            q = Poly(search.variables, dict(q_key))
            child = search.memo[q_key]
            recurse(q, child)
            cm = Poly.monomial(search.variables, c, m)
            ok = cm * q == p
            emit("content-extraction",
                 f"提取有理内容 {cm}: {p} = ({cm}) * ({q})",
                 DOMAIN_ALL,
                 f"规范展开 ({cm}) * ({q}) 逐项对比 == 原式", ok)
        elif kind == "split":
            _, a_key, b_key = choice
            pa = Poly(search.variables, dict(a_key))
            pb = Poly(search.variables, dict(b_key))
            recurse(pa, search.memo[a_key])
            recurse(pb, search.memo[b_key])
            ok = pa + pb == p
            emit("term-grouping",
                 f"项分组: {p} = ({pa}) + ({pb})",
                 DOMAIN_SUM,
                 "规范展开 (A) + (B) 逐项对比 == 原式", ok)
        else:
            raise ValueError(f"unknown choice: {choice!r}")

    if entry.node is None:
        emit("incomplete", "预算耗尽, 未找到完整表达式", "—",
             "仅有上下界, 无表达式可核验", True)
        return steps, False

    recurse(poly, entry)
    expanded = search.dag.expand(entry.node, search.variables)
    final_ok = expanded == poly
    emit("final-expansion",
         f"expand({search.dag.to_string(entry.node)}) == {poly}",
         "—", "独立规范展开, 逐项严格相等", final_ok)
    return steps, final_ok and all(s.verified for s in steps)
