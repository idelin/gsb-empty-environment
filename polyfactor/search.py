"""有限候选分组上的精确搜索：最少运算次数的因子提取 DAG。

候选空间：项集的所有集合划分（Bell 数规模）。每个 >=2 项的块必须有
非平凡公因子（平凡因子等价于更细的划分，不损失最优性）；块的商式递归
优化。共享子表达式由 dag 模块 hash-consing，成本只计一次。

小项数：穷尽候选空间，给出本模型下的全局最优（非局部贪心）。
大预算中断：保留上界（当前最优完整候选）与下界（全体候选的可采纳
乐观估计的最小值），并明确不承诺最优。
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .dag import atom_cost, dag_cost, make_add, make_atom, make_mul
from .poly import Polynomial


class _BudgetExceeded(Exception):
    pass


@dataclass
class SearchResult:
    dag: tuple
    cost: int
    complete: bool  # True = 候选空间已穷尽，确为本成本模型下的全局最优
    upper_bound: int
    lower_bound: int
    explored: int
    budget: int


def _partitions(items):
    """集合划分枚举，粗划分（块更少）优先，利于尽早找到低成本候选。"""
    items = list(items)
    if not items:
        yield []
        return
    first, rest = items[0], items[1:]
    for p in _partitions(rest):
        for i in range(len(p)):
            yield p[:i] + [[first] + p[i]] + p[i + 1:]
        yield p + [[first]]


class _Optimizer:
    def __init__(self, root: Polynomial, budget: int):
        self.root = root
        self.budget = budget
        self.explored = 0
        self.memo: dict[Polynomial, tuple] = {}
        self.root_best = None  # 根节点已完整评估的最优候选 (cost, dag)

    def solve(self, poly: Polynomial):
        hit = self.memo.get(poly)
        if hit is not None:
            return hit
        n = poly.num_terms
        if n == 0:  # 零多项式：特殊处理，约定为常数 0 叶子
            res = (0, make_atom(Fraction(0), tuple(0 for _ in poly.vars)))
        elif n == 1:
            m, c = poly.terms[0]
            dag = make_atom(c, m)
            res = (dag_cost(dag), dag)
        else:
            res = self._search(poly)
        self.memo[poly] = res
        return res

    def _search(self, poly: Polynomial):
        terms = poly.terms
        best = None
        for partition in _partitions(range(len(terms))):
            blocks = []
            viable = True
            for blk in partition:
                if len(blk) >= 2:
                    sub = Polynomial(poly.vars, tuple(terms[i] for i in sorted(blk)))
                    g, mono = sub.common_factor()
                    if g == 1 and not any(mono):
                        viable = False  # 平凡公因子：等价于更细划分，跳过
                        break
                    blocks.append((g, mono, sub))
                else:
                    blocks.append(None)
            if not viable:
                continue
            self.explored += 1
            if self.explored > self.budget:
                raise _BudgetExceeded
            children = []
            for blk, info in zip(partition, blocks):
                if info is None:
                    m, c = terms[blk[0]]
                    children.append(make_atom(c, m))
                else:
                    g, mono, sub = info
                    _, qdag = self.solve(sub.div_term(g, mono))
                    children.append(make_mul([make_atom(g, mono), qdag]))
            dag = make_add(children)
            cost = dag_cost(dag)
            if best is None or cost < best[0]:
                best = (cost, dag)
            if poly == self.root and (self.root_best is None or cost < self.root_best[0]):
                self.root_best = (cost, dag)
        return best


def _est_lower_bound(poly: Polynomial, cap: int = 10) -> int:
    """全体根候选的可采纳下界的最小值（廉价，不递归）。

    任一候选成本 >= (块数-1) 次顶层加法
                 + (存在多项块 ? 至少 1 次提取乘法)
                 + (存在多项块 ? 至少 1 次商式内部加法，商式 >=2 项)
                 + 单例块叶成本之和（互不相同，必各计一次）。
    共享只可能进一步降低成本，故估计可采纳；最优候选的成本 >= 该最小值。
    """
    n = poly.num_terms
    if n < 2:
        return 0
    if n > cap:
        return 1
    terms = poly.terms
    best = None
    for partition in _partitions(range(n)):
        multi = any(len(b) >= 2 for b in partition)
        est = len(partition) - 1 + (2 if multi else 0)
        est += sum(
            atom_cost(terms[b[0]][1], terms[b[0]][0]) for b in partition if len(b) == 1
        )
        if best is None or est < best:
            best = est
    return best


def optimize(poly: Polynomial, budget: int = 10_000) -> SearchResult:
    """主入口：精确搜索；预算耗尽时中断并保留上下界。"""
    opt = _Optimizer(poly, budget)
    try:
        cost, dag = opt.solve(poly)
        return SearchResult(dag, cost, True, cost, cost, opt.explored, budget)
    except _BudgetExceeded:
        if opt.root_best is not None:
            cost, dag = opt.root_best
        else:  # 尚无完整候选：退化为平铺和（平凡但完备的候选）
            dag = make_add([make_atom(c, m) for m, c in poly.terms])
            cost = dag_cost(dag)
        lb = min(_est_lower_bound(poly), cost)
        return SearchResult(dag, cost, False, cost, lb, opt.explored, budget)
