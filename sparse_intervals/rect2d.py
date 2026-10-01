"""二维生效范围：业务时间 t × 系统版本时间 v 的规范矩形分区。

每条规则同时受业务时间区间 [t0, t1) 与系统版本时间区间 [v0, v1) 约束。
查询点为 (t, v)，规则选择顺序与一维一致：优先级 -> 版本 -> ID。

输出覆盖整个有限查询矩形的规范、不重叠矩形分区：
  - 坐标压缩为单元网格后按值合并；
  - 默认使用局部贪心合并（不宣称全局最少）；
  - 可选精确最小矩形覆盖求解器（小网格，带预算中断界）；
  - 撤销规则时仅失效其覆盖区域，并用全量网格枚举逐单元、逐端点对照。
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Hashable, Iterable, Optional

from .solver import (
    components_lower_bound,
    greedy_partition,
    min_rect_partition,
    verify_partition_continuous,
)


@dataclass(frozen=True)
class Rule2D:
    """二维规则：业务时间 [t0, t1) × 系统版本时间 [v0, v1)，均为半开。"""

    rid: str
    priority: int
    version: int
    t0: float
    t1: float
    v0: float
    v1: float
    value: Hashable

    def __post_init__(self) -> None:
        if not (self.t0 < self.t1 and self.v0 < self.v1):
            raise ValueError(
                f"rule {self.rid!r}: empty rect "
                f"t=[{self.t0}, {self.t1}) v=[{self.v0}, {self.v1})"
            )

    def rank_key(self) -> tuple:
        return (-self.priority, -self.version, self.rid)

    def covers_point(self, t: float, v: float) -> bool:
        return self.t0 <= t < self.t1 and self.v0 <= v < self.v1


class RectEngine:
    """二维规范矩形分区引擎，支持增量撤销与全量对照验证。"""

    def __init__(self, t0: float, t1: float, v0: float, v1: float) -> None:
        if not (t0 < t1 and v0 < v1):
            raise ValueError("empty query rect")
        self.query = (t0, t1, v0, v1)
        self._rules: dict[str, Rule2D] = {}
        self._built = False
        self._xs: list = []
        self._ys: list = []
        self._cells: list = []
        self._winners: list = []
        self._points: list = []

    def add(self, rule: Rule2D) -> None:
        if rule.rid in self._rules:
            raise ValueError(f"duplicate rule id: {rule.rid!r}")
        self._rules[rule.rid] = rule
        self._built = False

    def rule_ids(self) -> list:
        return sorted(self._rules)

    def winner_at(self, t: float, v: float) -> Optional[Rule2D]:
        best: Optional[Rule2D] = None
        for r in self._rules.values():
            if r.covers_point(t, v) and (
                best is None or r.rank_key() < best.rank_key()
            ):
                best = r
        return best

    def value_at(self, t: float, v: float) -> Any:
        w = self.winner_at(t, v)
        return w.value if w is not None else None

    def build(self) -> None:
        t0, t1, v0, v1 = self.query
        xs = {t0, t1}
        ys = {v0, v1}
        for r in self._rules.values():
            a, b = max(r.t0, t0), min(r.t1, t1)
            if a < b:
                xs.add(a)
                xs.add(b)
            a, b = max(r.v0, v0), min(r.v1, v1)
            if a < b:
                ys.add(a)
                ys.add(b)
        self._xs = sorted(xs)
        self._ys = sorted(ys)
        self._recompute_all()
        self._built = True

    def _recompute_all(self) -> None:
        xs, ys = self._xs, self._ys
        self._cells = []
        self._winners = []
        for j in range(len(ys) - 1):
            vm = (ys[j] + ys[j + 1]) / 2
            row, wrow = [], []
            for i in range(len(xs) - 1):
                tm = (xs[i] + xs[i + 1]) / 2
                w = self.winner_at(tm, vm)
                row.append(w.value if w is not None else None)
                wrow.append(w.rid if w is not None else None)
            self._cells.append(row)
            self._winners.append(wrow)
        self._points = [[self.value_at(x, y) for x in xs] for y in ys]

    def _ensure(self) -> None:
        if not self._built:
            self.build()

    def _to_coord_rect(self, cr: tuple) -> dict:
        x0, y0, x1, y1, value = cr
        return {"t0": self._xs[x0], "v0": self._ys[y0],
                "t1": self._xs[x1], "v1": self._ys[y1], "value": value}

    def partition(self) -> list:
        """贪心合并的规范分区。注意：局部贪心，不保证全局最少矩形数。"""
        self._ensure()
        return [self._to_coord_rect(r) for r in greedy_partition(self._cells)]

    def exact_partition(self, budget: int = 200_000) -> dict:
        """精确最小矩形覆盖（小网格）。status 非 "optimal" 时不保证最少。"""
        self._ensure()
        rects, status, nodes = min_rect_partition(self._cells, budget)
        return {
            "rects": [self._to_coord_rect(r) for r in rects],
            "rect_count": len(rects),
            "status": status,
            "nodes": nodes,
            "budget": budget,
            "lower_bound_components": components_lower_bound(self._cells),
        }

    def grid_dump(self) -> dict:
        self._ensure()
        return {
            "xs": list(self._xs),
            "ys": list(self._ys),
            "cells": [list(row) for row in self._cells],
            "winners": [list(row) for row in self._winners],
        }

    def verify(self, rects: Optional[list] = None) -> list:
        """用独立验证器检查给定分区（默认当前贪心分区）。"""
        self._ensure()
        if rects is None:
            rects = self.partition()
        return verify_partition_continuous(self.value_at, self.query, rects)

    def revoke(self, rid: str, verify: bool = True) -> dict:
        """撤销规则：仅失效其覆盖区域并重算，其余缓存单元/端点保留。

        verify=True 时以全量网格枚举对照每个单元及每个端点。
        返回 {"rid", "affected", "verification"}。
        """
        self._ensure()
        rule = self._rules.get(rid)
        if rule is None:
            raise KeyError(f"unknown rule id: {rid!r}")
        del self._rules[rid]

        t0, t1, v0, v1 = self.query
        rt0, rt1 = max(rule.t0, t0), min(rule.t1, t1)
        rv0, rv1 = max(rule.v0, v0), min(rule.v1, v1)
        xs, ys = self._xs, self._ys
        W, H = len(xs) - 1, len(ys) - 1

        changed = []
        if rt0 < rt1 and rv0 < rv1:
            for j in range(H):
                if ys[j + 1] <= rv0 or ys[j] >= rv1:
                    continue
                vm = (ys[j] + ys[j + 1]) / 2
                for i in range(W):
                    if xs[i + 1] <= rt0 or xs[i] >= rt1:
                        continue
                    tm = (xs[i] + xs[i + 1]) / 2
                    w = self.winner_at(tm, vm)
                    nv = w.value if w is not None else None
                    if nv != self._cells[j][i]:
                        changed.append((i, j, self._cells[j][i], nv))
                        self._cells[j][i] = nv
                        self._winners[j][i] = w.rid if w is not None else None
            for j, y in enumerate(ys):
                for i, x in enumerate(xs):
                    if rule.covers_point(x, y):
                        self._points[j][i] = self.value_at(x, y)

        result = {
            "rid": rid,
            "affected": _changed_cells_to_rects(changed, xs, ys),
        }

        if verify:
            mismatches = []
            for j in range(H):
                vm = (ys[j] + ys[j + 1]) / 2
                for i in range(W):
                    tm = (xs[i] + xs[i + 1]) / 2
                    fv = self.value_at(tm, vm)
                    if fv != self._cells[j][i]:
                        mismatches.append({
                            "type": "cell", "i": i, "j": j,
                            "incremental": self._cells[j][i], "full": fv,
                        })
            for j, y in enumerate(ys):
                for i, x in enumerate(xs):
                    fv = self.value_at(x, y)
                    if fv != self._points[j][i]:
                        mismatches.append({
                            "type": "point", "t": x, "v": y,
                            "incremental": self._points[j][i], "full": fv,
                        })
            result["verification"] = {
                "checked_cells": W * H,
                "checked_points": len(xs) * len(ys),
                "mismatches": mismatches,
            }
        return result


def _changed_cells_to_rects(changed: list, xs: list, ys: list) -> list:
    """把变化单元按 (old, new) 分组并贪心合并为坐标矩形。"""
    W, H = len(xs) - 1, len(ys) - 1
    by_pair: dict[tuple, list] = {}
    for i, j, o, n in changed:
        by_pair.setdefault((o, n), []).append((i, j))
    out = []
    for (o, n), cells in by_pair.items():
        g = [[("u", i, j) for i in range(W)] for j in range(H)]
        for (i, j) in cells:
            g[j][i] = "X"
        for (x0, y0, x1, y1, val) in greedy_partition(g):
            if val == "X":
                out.append({
                    "t0": xs[x0], "v0": ys[y0],
                    "t1": xs[x1], "v1": ys[y1],
                    "old_value": o, "new_value": n,
                })
    out.sort(key=lambda r: (r["t0"], r["v0"], r["t1"], r["v1"]))
    return out
