#!/usr/bin/env python3
"""跨日班次最小人员覆盖求解器（仅使用 Python 标准库）。

在一周循环（7 * 24 * 60 = 10080 分钟）的时间轴上，给定分钟级需求区间
与候选班次（可跨周末、最长一天），为每个班次选择非负整数人数，使得所有
时段需求被满足，并依次最小化：
  1. 总成本；
  2. 总人数；
  3. 按班次 ID 排序的人数向量（字典序）。

算法：分支限界。下界来自覆盖线性规划的对偶可行解（合法下界）。
支持节点数 / 时间预算中断：中断时返回当前最优可行解（上界）与合法下界，
只有搜索完整结束（或下界严格大于上界）时才声明最优。
"""
from __future__ import annotations

import argparse
import json
import math
import sys
import time
from fractions import Fraction

WEEK_MINUTES = 7 * 24 * 60  # 一周 10080 分钟
MAX_SHIFT_MINUTES = 24 * 60  # 班次最长一天


class InputError(ValueError):
    """输入数据不合法。"""


def _check_minute(value, name):
    if not isinstance(value, int) or isinstance(value, bool):
        raise InputError(f"{name} 必须是整数分钟，得到 {value!r}")
    if not 0 <= value < WEEK_MINUTES:
        raise InputError(f"{name}={value} 超出 [0, {WEEK_MINUTES})")


def _interval_duration(start, end):
    return (end - start) % WEEK_MINUTES


def _covers(start, end, t):
    """半开区间 [start, end)（end <= start 表示跨周末）是否覆盖时刻 t。"""
    if start < end:
        return start <= t < end
    return t >= start or t < end


def _parse_input(data):
    if not isinstance(data, dict):
        raise InputError("顶层必须是 JSON 对象")
    raw_demands = data.get("demands")
    raw_shifts = data.get("shifts")
    if not isinstance(raw_demands, list) or not isinstance(raw_shifts, list):
        raise InputError("demands 与 shifts 必须是数组")

    demands = []
    for i, d in enumerate(raw_demands):
        if not isinstance(d, dict):
            raise InputError(f"demands[{i}] 必须是对象")
        try:
            start, end, req = d["start"], d["end"], d["required"]
        except KeyError as exc:
            raise InputError(f"demands[{i}] 缺少字段 {exc}") from exc
        _check_minute(start, f"demands[{i}].start")
        _check_minute(end, f"demands[{i}].end")
        if start == end:
            raise InputError(f"demands[{i}] 起止相同，区间为空")
        if not isinstance(req, int) or isinstance(req, bool) or req < 0:
            raise InputError(f"demands[{i}].required 必须是非负整数")
        if req > 0:
            demands.append((start, end, req))

    shifts = []
    seen_ids = set()
    for i, s in enumerate(raw_shifts):
        if not isinstance(s, dict):
            raise InputError(f"shifts[{i}] 必须是对象")
        try:
            sid, start, end, cost = s["id"], s["start"], s["end"], s["cost"]
        except KeyError as exc:
            raise InputError(f"shifts[{i}] 缺少字段 {exc}") from exc
        if not isinstance(sid, str) or not sid:
            raise InputError(f"shifts[{i}].id 必须是非空字符串")
        if sid in seen_ids:
            raise InputError(f"班次 id 重复: {sid!r}")
        seen_ids.add(sid)
        _check_minute(start, f"shifts[{i}].start")
        _check_minute(end, f"shifts[{i}].end")
        duration = _interval_duration(start, end)
        if duration == 0:
            raise InputError(f"班次 {sid!r} 起止相同，区间为空")
        if duration > MAX_SHIFT_MINUTES:
            raise InputError(
                f"班次 {sid!r} 时长 {duration} 分钟，超过一天上限 {MAX_SHIFT_MINUTES}"
            )
        if not isinstance(cost, int) or isinstance(cost, bool) or cost < 0:
            raise InputError(f"班次 {sid!r} 的 cost 必须是非负整数")
        shifts.append({"id": sid, "start": start, "end": end, "cost": cost})
    return demands, shifts


def _build_model(demands, shifts):
    """把时间轴切成基本段，计算每段需求与每个班次覆盖的段集合。"""
    points = set()
    for start, end, _ in demands:
        points.update((start, end))
    for s in shifts:
        points.update((s["start"], s["end"]))
    pts = sorted(points)
    seg_ranges = []  # (a, b)，最后一段可能跨周末
    mids = []
    for a, b in zip(pts, pts[1:]):
        seg_ranges.append((a, b))
        mids.append((a + b) / 2.0)
    if pts:
        seg_ranges.append((pts[-1], pts[0]))
        wrap_len = WEEK_MINUTES - pts[-1] + pts[0]
        mids.append((pts[-1] + wrap_len / 2.0) % WEEK_MINUTES)

    seg_demand = []
    for mid in mids:
        req = 0
        for start, end, r in demands:
            if _covers(start, end, mid):
                req = max(req, r)
        seg_demand.append(req)
    for s in shifts:
        s["segs"] = [
            i for i, mid in enumerate(mids) if _covers(s["start"], s["end"], mid)
        ]
    return seg_demand, seg_ranges


class BranchAndBoundSolver:
    """整数分支限界求解器。"""

    def __init__(self, seg_demand, shifts, node_limit=None, time_limit=None):
        self.seg_demand = list(seg_demand)
        self.shifts = shifts
        self.node_limit = node_limit
        self.time_limit = time_limit
        n_seg = len(self.seg_demand)
        self.seg_shifts = [[] for _ in range(n_seg)]
        for j, s in enumerate(shifts):
            for i in s["segs"]:
                self.seg_shifts[i].append(j)
        # 对偶可行权重 y_i = min_{j 覆盖 i} cost_j / |S_j|：
        # 对任意班次 j，sum_{i in S_j} y_i <= cost_j，故 sum rem_i * y_i
        # 是剩余问题成本的合法下界（覆盖 LP 的对偶可行解）。
        self.seg_weight = []
        for i in range(n_seg):
            best = None
            for j in self.seg_shifts[i]:
                cand = Fraction(shifts[j]["cost"], max(1, len(shifts[j]["segs"])))
                if best is None or cand < best:
                    best = cand
            self.seg_weight.append(best if best is not None else Fraction(0))
        self.rem = list(self.seg_demand)
        self.counts = [0] * len(shifts)
        self.cur_cost = 0
        self.cur_staff = 0
        self.nodes = 0
        self.budget_exhausted = False
        self.frontier_lb = None  # 预算截断时，未展开节点下界的最小值
        self.best = None
        self._id_order = sorted(range(len(shifts)), key=lambda j: shifts[j]["id"])

    def uncovered_segments(self):
        return [
            i
            for i, r in enumerate(self.seg_demand)
            if r > 0 and not self.seg_shifts[i]
        ]

    def _apply(self, j):
        s = self.shifts[j]
        self.counts[j] += 1
        self.cur_cost += s["cost"]
        self.cur_staff += 1
        for i in s["segs"]:
            self.rem[i] -= 1

    def _unapply(self, j):
        s = self.shifts[j]
        self.counts[j] -= 1
        self.cur_cost -= s["cost"]
        self.cur_staff -= 1
        for i in s["segs"]:
            self.rem[i] += 1

    def _first_uncovered(self):
        for i, r in enumerate(self.rem):
            if r > 0:
                return i
        return None

    def _lb_remaining(self):
        total = Fraction(0)
        for i, r in enumerate(self.rem):
            if r > 0:
                total += r * self.seg_weight[i]
        return total

    def _lb_cost(self):
        return self.cur_cost + math.ceil(self._lb_remaining())

    def _record_frontier(self, lb_cost):
        if self.frontier_lb is None or lb_cost < self.frontier_lb:
            self.frontier_lb = lb_cost

    def _prunable(self, lb_cost):
        best = self.best
        if best is None:
            return False
        if lb_cost > best["cost"]:
            return True
        # 成本无法更优且人数也不可能更少（至少还要再开一个班）时剪枝。
        if lb_cost == best["cost"] and self.cur_staff >= best["staff"]:
            return True
        return False

    def _solution_key(self, counts):
        vec = tuple(counts[j] for j in self._id_order)
        cost = sum(counts[j] * self.shifts[j]["cost"] for j in range(len(counts)))
        staff = sum(counts)
        return cost, staff, vec

    def _record_solution(self):
        key = self._solution_key(self.counts)
        if self.best is None or key < self.best["key"]:
            self.best = {"key": key, "cost": key[0], "staff": key[1],
                         "counts": list(self.counts)}

    def _greedy(self):
        """贪心构造一个可行解作为初始上界；不可行时返回 None。"""
        rem = list(self.seg_demand)
        counts = [0] * len(self.shifts)
        while True:
            target = next((i for i, r in enumerate(rem) if r > 0), None)
            if target is None:
                return counts
            best_j = None
            best_key = None
            for j in self.seg_shifts[target]:
                s = self.shifts[j]
                gain = sum(1 for i in s["segs"] if rem[i] > 0)
                if gain == 0:
                    continue
                key = (Fraction(s["cost"], gain), -gain, j)
                if best_key is None or key < best_key:
                    best_key = key
                    best_j = j
            if best_j is None:
                return None
            counts[best_j] += 1
            for i in self.shifts[best_j]["segs"]:
                if rem[i] > 0:
                    rem[i] -= 1

    def _check_budget(self):
        if self.node_limit is not None and self.nodes > self.node_limit:
            self.budget_exhausted = True
        if (
            self.time_limit is not None
            and (self.nodes & 0x1FF) == 1
            and time.monotonic() - self._t0 > self.time_limit
        ):
            self.budget_exhausted = True

    def _dfs(self, last_idx):
        self.nodes += 1
        self._check_budget()
        target = self._first_uncovered()
        if target is None:
            self._record_solution()
            return
        lb_cost = self._lb_cost()
        if self.budget_exhausted:
            self._record_frontier(lb_cost)
            return
        if self._prunable(lb_cost):
            return
        for j in self.seg_shifts[target]:
            if j < last_idx:
                continue
            if self.budget_exhausted:
                # 预算已尽：只记录子节点下界，不再展开。
                self._apply(j)
                self._record_frontier(self._lb_cost())
                self._unapply(j)
                continue
            self._apply(j)
            self._dfs(j)
            self._unapply(j)

    def solve(self):
        self._t0 = time.monotonic()
        sys.setrecursionlimit(max(10000, sum(self.seg_demand) + 1000))
        greedy_counts = self._greedy()
        if greedy_counts is not None:
            key = self._solution_key(greedy_counts)
            self.best = {"key": key, "cost": key[0], "staff": key[1],
                         "counts": list(greedy_counts)}
        self._dfs(0)
        elapsed = time.monotonic() - self._t0

        proven = False
        if self.best is not None:
            if not self.budget_exhausted:
                proven = True
            elif self.frontier_lb is not None and self.frontier_lb > self.best["cost"]:
                # 所有未展开子树的成本下界都严格大于当前解：成本与平局项均已确定。
                proven = True
        if proven:
            status = "optimal"
            lower = self.best["cost"]
            message = "分支限界搜索完整结束（或下界严格超过上界），已证明最优。"
        elif self.best is not None:
            status = "feasible"
            lower = self.frontier_lb if self.frontier_lb is not None else 0
            message = (
                "预算耗尽，搜索未完成：返回当前最优可行解（上界）与合法下界，"
                "最优性未证明。"
            )
        else:
            status = "no_solution"
            lower = self.frontier_lb if self.frontier_lb is not None else 0
            message = "预算耗尽且尚未找到可行解。"

        result = {
            "status": status,
            "proven_optimal": proven,
            "lower_bound": lower,
            "upper_bound": self.best["cost"] if self.best is not None else None,
            "nodes": self.nodes,
            "elapsed_seconds": round(elapsed, 6),
            "message": message,
        }
        if self.best is not None:
            result["total_cost"] = self.best["cost"]
            result["total_staff"] = self.best["staff"]
            result["assignment"] = {
                self.shifts[j]["id"]: self.best["counts"][j]
                for j in range(len(self.shifts))
                if self.best["counts"][j] > 0
            }
        return result


def solve_instance(demands, shifts, node_limit=None, time_limit=None):
    seg_demand, seg_ranges = _build_model(demands, shifts)
    solver = BranchAndBoundSolver(seg_demand, shifts, node_limit, time_limit)
    bad = [seg_ranges[i] for i in solver.uncovered_segments()]
    if bad:
        return {
            "status": "infeasible",
            "proven_optimal": False,
            "lower_bound": None,
            "upper_bound": None,
            "nodes": 0,
            "elapsed_seconds": 0.0,
            "uncovered_segments": [[a, b] for a, b in bad],
            "message": "存在无任何班次覆盖的需求段，问题不可行。",
        }
    return solver.solve()


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="跨日班次最小人员覆盖求解器（分支限界，仅标准库）"
    )
    parser.add_argument("input", nargs="?", help="输入 JSON 文件，缺省读标准输入")
    parser.add_argument("--time-limit", type=float, default=None,
                        help="时间预算（秒），覆盖输入文件中的设置")
    parser.add_argument("--node-limit", type=int, default=None,
                        help="分支限界节点数预算，覆盖输入文件中的设置")
    parser.add_argument("--compact", action="store_true", help="单行 JSON 输出")
    args = parser.parse_args(argv)

    try:
        if args.input:
            with open(args.input, "r", encoding="utf-8") as fh:
                data = json.load(fh)
        else:
            data = json.load(sys.stdin)
    except (OSError, json.JSONDecodeError) as exc:
        print(f"输入读取/解析失败: {exc}", file=sys.stderr)
        return 2

    try:
        demands, shifts = _parse_input(data)
    except InputError as exc:
        print(f"输入不合法: {exc}", file=sys.stderr)
        return 2

    node_limit = args.node_limit
    if node_limit is None:
        node_limit = data.get("node_limit")
    time_limit = args.time_limit
    if time_limit is None:
        time_limit = data.get("time_limit_seconds")

    result = solve_instance(demands, shifts, node_limit, time_limit)
    indent = None if args.compact else 2
    print(json.dumps(result, ensure_ascii=False, indent=indent))
    return 0 if result["status"] in ("optimal", "feasible") else 1


if __name__ == "__main__":
    sys.exit(main())
