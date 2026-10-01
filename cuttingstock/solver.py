"""小规模精确分支搜索（branch & bound）求解器，不调用任何外部求解器。

目标（字典序）：先最小化毛损耗（消耗原料总长 - 需求总长），再最小化原料成本。
即切割方案按"损耗再原料成本"排序取最优。

- 原料规格为有限集合，每种规格可限量或不限量；余料以 0 成本限量规格参与优化。
- 需求数量逐项精确满足（不多切、不少切），库存消耗不超过限量。
- 预算（搜索节点数）耗尽时返回当前最优可行解并标记"未证最优"。
"""

from __future__ import annotations

import sys
from fractions import Fraction
from math import gcd

from .model import (
    BarCut,
    Demand,
    SolveResult,
    Status,
    StockSpec,
    piece_fits,
    to_fraction,
)


def _lcm(a: int, b: int) -> int:
    return a // gcd(a, b) * b


class Solver:
    """对单批需求在给定库存下做精确分支搜索。"""

    def __init__(self, demands, stocks, kerf=0, min_remnant=0, max_nodes=100_000):
        self.demands = [d if isinstance(d, Demand) else Demand.of(*d) for d in demands]
        self.demands = [d for d in self.demands if d.quantity > 0]
        self.stocks = [s if isinstance(s, StockSpec) else StockSpec.of(*s) for s in stocks]
        self.kerf = to_fraction(kerf)
        self.min_remnant = to_fraction(min_remnant)
        self.max_nodes = int(max_nodes)
        for d in self.demands:
            if d.length <= 0:
                raise ValueError("需求长度必须为正")
        if self.demands and not self.stocks:
            raise ValueError("有需求但没有任何原料规格")
        for s in self.stocks:
            if s.length <= 0:
                raise ValueError("原料长度必须为正")
        self.nodes = 0
        self.budget_exhausted = False

    # ------------------------------------------------------------------
    # 下界：覆盖 DP（把各规格视为不限量，求覆盖需求总长所需的最小原料总长）
    # ------------------------------------------------------------------
    def _lower_bound_waste(self, total_demand: Fraction) -> Fraction:
        if total_demand <= 0 or not self.stocks:
            self._scale = 1
            self._cover = [0]
            self._dem_scaled = []
            return Fraction(0)
        scale = 1
        for v in [s.length for s in self.stocks] + [d.length for d in self.demands]:
            scale = _lcm(scale, v.denominator)
        need = int(total_demand * scale)
        lens = [int(s.length * scale) for s in self.stocks]
        cover = [0] * (need + 1)
        for x in range(1, need + 1):
            best = None
            for sl in lens:
                cand = sl + (cover[x - sl] if x > sl else 0)
                if best is None or cand < best:
                    best = cand
            cover[x] = best
        self._scale = scale
        self._cover = cover
        self._dem_scaled = [int(d.length * scale) for d in self.demands]
        return Fraction(cover[need], scale) - total_demand

    # ------------------------------------------------------------------
    # 切割模式枚举：某规格原料上所有可行的需求段组合
    # ------------------------------------------------------------------
    def _enumerate_patterns(self, stock_length: Fraction):
        lengths = [d.length for d in self.demands]
        caps = [d.quantity for d in self.demands]
        kerf = self.kerf
        n_types = len(lengths)
        patterns = []  # (counts tuple, cuts, leftover)
        counts = [0] * n_types

        def rec(i, used, npieces):
            if i == n_types:
                if npieces == 0:
                    return
                if used + kerf * npieces <= stock_length:
                    patterns.append(
                        (tuple(counts), npieces, stock_length - used - kerf * npieces)
                    )
                elif used + kerf * (npieces - 1) == stock_length:
                    patterns.append((tuple(counts), npieces - 1, Fraction(0)))
                return
            li = lengths[i]
            c, u, n = 0, used, npieces
            while c <= caps[i]:
                counts[i] = c
                rec(i + 1, u, n)
                c += 1
                u += li
                n += 1
                # 再加任何段都不可行（普通放法与恰好用尽放法都被排除）时剪枝
                if u + kerf * max(0, n - 1) > stock_length:
                    break
            counts[i] = 0

        rec(0, Fraction(0), 0)
        return patterns

    # ------------------------------------------------------------------
    # 主求解
    # ------------------------------------------------------------------
    def solve(self) -> SolveResult:
        kerf = self.kerf
        total_demand = sum((d.length * d.quantity for d in self.demands), Fraction(0))
        lb_waste = self._lower_bound_waste(total_demand)

        # 超长需求直接不可行（无需搜索）
        for d in self.demands:
            if not any(piece_fits(d.length, s.length, kerf) for s in self.stocks):
                return SolveResult(
                    Status.INFEASIBLE, [], None, None, None, (), None, lb_waste, 0, False
                )
        if not self.demands:
            return SolveResult(
                Status.OPTIMAL, [], Fraction(0), Fraction(0), Fraction(0),
                (), Fraction(0), lb_waste, 0, False,
            )

        quantities = [d.quantity for d in self.demands]
        dem_lens = [d.length for d in self.demands]
        n_types = len(self.demands)
        scale = self._scale
        dem_scaled = self._dem_scaled
        cover = self._cover

        # 每种原料长度的切割模式，按需求类型索引，按 (余料, 段数) 排序
        pats_by_type = {}
        for s in self.stocks:
            if s.length in pats_by_type:
                continue
            pats = self._enumerate_patterns(s.length)
            by_type = [[] for _ in range(n_types)]
            for p in pats:
                for i, c in enumerate(p[0]):
                    if c:
                        by_type[i].append(p)
            for lst in by_type:
                lst.sort(key=lambda p: (p[2], -sum(p[0])))
            pats_by_type[s.length] = by_type

        # 限量规格的库存槽位
        limited_pos = {}
        start_avail = []
        for si, s in enumerate(self.stocks):
            if s.count is not None:
                limited_pos[si] = len(start_avail)
                start_avail.append(s.count)

        best_waste = None
        best_cost = None
        best_bars = None
        memo = {}
        remaining = list(quantities)
        avail = list(start_avail)
        bars = []
        budget = self.max_nodes

        def dfs(consumed, cost):
            nonlocal best_waste, best_cost, best_bars
            self.nodes += 1
            if self.nodes > budget:
                self.budget_exhausted = True
                return
            bi = -1
            for i in range(n_types):
                if remaining[i] > 0:
                    bi = i
                    break
            if bi < 0:  # 叶子：需求全部满足
                waste = consumed - total_demand
                if (
                    best_waste is None
                    or waste < best_waste
                    or (waste == best_waste and cost < best_cost)
                ):
                    best_waste, best_cost = waste, cost
                    best_bars = list(bars)
                return
            if best_waste is not None:  # 下界剪枝
                need_x = 0
                for i in range(bi, n_types):
                    if remaining[i]:
                        need_x += remaining[i] * dem_scaled[i]
                lbw = consumed + Fraction(cover[need_x], scale) - total_demand
                if lbw > best_waste or (lbw == best_waste and cost >= best_cost):
                    return
            state = (tuple(remaining), tuple(avail))
            prev = memo.get(state)
            if prev is not None and (
                prev[0] < consumed or (prev[0] == consumed and prev[1] <= cost)
            ):
                return
            memo[state] = (consumed, cost)
            # 候选 (原料源, 模式)，按 (余料, 成本) 排序，优先探索低损耗低成本
            cands = []
            for si, s in enumerate(self.stocks):
                lp = limited_pos.get(si)
                if lp is not None and avail[lp] == 0:
                    continue
                for p in pats_by_type[s.length][bi]:
                    counts = p[0]
                    ok = True
                    for j in range(n_types):
                        if counts[j] > remaining[j]:
                            ok = False
                            break
                    if ok:
                        cands.append((p[2], s.cost, si, p))
            cands.sort(key=lambda t: (t[0], t[1]))
            for _, _, si, p in cands:
                s = self.stocks[si]
                counts = p[0]
                for j in range(n_types):
                    if counts[j]:
                        remaining[j] -= counts[j]
                lp = limited_pos.get(si)
                if lp is not None:
                    avail[lp] -= 1
                bars.append((si, p))
                dfs(consumed + s.length, cost + s.cost)
                bars.pop()
                if lp is not None:
                    avail[lp] += 1
                for j in range(n_types):
                    if counts[j]:
                        remaining[j] += counts[j]
                if self.budget_exhausted:
                    return

        old_limit = sys.getrecursionlimit()
        if old_limit < 10000:
            sys.setrecursionlimit(10000)
        try:
            dfs(Fraction(0), Fraction(0))
        finally:
            sys.setrecursionlimit(old_limit)

        if best_bars is None:
            status = Status.UNKNOWN if self.budget_exhausted else Status.INFEASIBLE
            return SolveResult(
                status, [], None, None, None, (), None, lb_waste,
                self.nodes, self.budget_exhausted,
            )

        out = []
        for si, p in best_bars:
            s = self.stocks[si]
            counts, cuts, leftover = p
            pieces = []
            for j, c in enumerate(counts):
                pieces.extend([dem_lens[j]] * c)
            out.append(BarCut(s.length, s.cost, tuple(pieces), cuts, leftover))
        remnants = tuple(
            sorted(
                (b.leftover for b in out if b.leftover > 0 and b.leftover >= self.min_remnant),
                reverse=True,
            )
        )
        consumed = sum((b.stock_length for b in out), Fraction(0))
        waste = consumed - total_demand
        net = waste - sum(remnants, Fraction(0))
        status = Status.FEASIBLE if self.budget_exhausted else Status.OPTIMAL
        return SolveResult(
            status, out, waste, best_cost, consumed, remnants, net, lb_waste,
            self.nodes, self.budget_exhausted,
        )


def solve(demands, stocks, kerf=0, min_remnant=0, max_nodes=100_000) -> SolveResult:
    """便捷入口：单次精确求解。"""
    return Solver(demands, stocks, kerf, min_remnant, max_nodes).solve()
