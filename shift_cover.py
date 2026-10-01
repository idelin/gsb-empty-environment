#!/usr/bin/env python3
"""跨日班次最小人员覆盖求解器（仅使用 Python 标准库，兼容 3.11+）。

问题定义：
  一周 7*24*60 = 10080 分钟循环。给定若干需求区间（每分钟所需人数）、
  候选班次（起始分钟 + 时长，时长 <= 1440 分钟，可跨日/跨周末循环）、
  每班成本。每个班次可安排非负整数个人，每人计 1 单位覆盖。
  目标：总成本最小；成本相同取总人数最少；再相同取按班次 ID 升序的
  人数向量字典序最小者。

算法：分支限界（best-first）。下界来自覆盖 LP 对偶的可行解
  y_t = min_i(cost_i / len_i)，对任意剩余需求均为合法下界（Fraction 精确计算）。
预算（节点数 / 秒数）耗尽时输出当前可行上界与合法下界，绝不虚报最优。
"""
from __future__ import annotations

import argparse
import heapq
import itertools
import json
import sys
import time
from fractions import Fraction
from typing import NamedTuple

WEEK_MINUTES = 7 * 24 * 60  # 10080，分钟 0 = 周一 00:00
MAX_SHIFT_MINUTES = 24 * 60  # 班次最长一天


class InputError(ValueError):
    """输入实例不合法。"""


class Shift(NamedTuple):
    sid: str
    start: int
    duration: int
    cost: Fraction
    minutes: tuple  # 覆盖的分钟（模一周）


class Instance(NamedTuple):
    demand: list          # 长度 10080 的每分钟需求人数（区间重叠取 max）
    shifts: list          # 按班次 ID 升序
    minute_to_shifts: list  # 每分钟 -> 覆盖它的班次下标列表


def _as_non_neg_int(value, what):
    if isinstance(value, bool) or not isinstance(value, int):
        raise InputError(f"{what} 必须是非负整数，得到 {value!r}")
    if value < 0:
        raise InputError(f"{what} 必须非负，得到 {value!r}")
    return value


def _as_cost(value, what):
    if isinstance(value, bool) or not isinstance(value, (int, float, str)):
        raise InputError(f"{what} 必须是数字，得到 {value!r}")
    try:
        cost = Fraction(value) if not isinstance(value, float) else Fraction(str(value))
    except (ValueError, ZeroDivisionError):
        raise InputError(f"{what} 无法解析为数字：{value!r}") from None
    if cost < 0:
        raise InputError(f"{what} 必须非负，得到 {value!r}")
    return cost


def parse_instance(data) -> Instance:
    if not isinstance(data, dict):
        raise InputError("输入必须是 JSON 对象")
    demand = [0] * WEEK_MINUTES
    demands = data.get("demands", [])
    if not isinstance(demands, list):
        raise InputError("demands 必须是数组")
    for k, d in enumerate(demands):
        try:
            start = _as_non_neg_int(d["start"], f"demands[{k}].start")
            end = _as_non_neg_int(d["end"], f"demands[{k}].end")
            required = _as_non_neg_int(d["required"], f"demands[{k}].required")
        except KeyError as e:
            raise InputError(f"demands[{k}] 缺少字段 {e}") from None
        if start >= WEEK_MINUTES or end > WEEK_MINUTES:
            raise InputError(f"demands[{k}] 区间越界 [0,{WEEK_MINUTES}]")
        if end == start:
            raise InputError(f"demands[{k}] 区间为空（end == start）")
        if end > start:
            rng = range(start, end)
        else:  # 跨周末回绕
            rng = itertools.chain(range(start, WEEK_MINUTES), range(0, end))
        for t in rng:
            if required > demand[t]:
                demand[t] = required

    raw_shifts = data.get("shifts", [])
    if not isinstance(raw_shifts, list):
        raise InputError("shifts 必须是数组")
    seen = set()
    shifts = []
    for k, s in enumerate(raw_shifts):
        try:
            sid = s["id"]
            start = _as_non_neg_int(s["start"], f"shifts[{k}].start")
            duration = s["duration"]
            cost = _as_cost(s["cost"], f"shifts[{k}].cost")
        except KeyError as e:
            raise InputError(f"shifts[{k}] 缺少字段 {e}") from None
        if not isinstance(sid, str) or not sid:
            raise InputError(f"shifts[{k}].id 必须是非空字符串")
        if sid in seen:
            raise InputError(f"班次 id 重复：{sid!r}")
        seen.add(sid)
        if start >= WEEK_MINUTES:
            raise InputError(f"班次 {sid!r} 的 start 越界 [0,{WEEK_MINUTES})")
        if isinstance(duration, bool) or not isinstance(duration, int) \
                or not 1 <= duration <= MAX_SHIFT_MINUTES:
            raise InputError(
                f"班次 {sid!r} 的 duration 必须是 [1,{MAX_SHIFT_MINUTES}] 的整数")
        minutes = tuple((start + off) % WEEK_MINUTES for off in range(duration))
        shifts.append(Shift(sid, start, duration, cost, minutes))
    shifts.sort(key=lambda sh: sh.sid)

    minute_to_shifts = [[] for _ in range(WEEK_MINUTES)]
    for i, sh in enumerate(shifts):
        for t in sh.minutes:
            minute_to_shifts[t].append(i)
    return Instance(demand, shifts, minute_to_shifts)


def _num(frac: Fraction):
    """Fraction -> int（整数值）或 float。"""
    if frac.denominator == 1:
        return frac.numerator
    return float(frac)


class Solver:
    def __init__(self, inst: Instance):
        self.inst = inst
        n = len(inst.shifts)
        # 对偶可行权值 m_t = min_i(cost_i/len_i)，用于合法成本下界
        self.min_cost_per_minute = []
        self.infeasible_reason = None
        for t in range(WEEK_MINUTES):
            covering = inst.minute_to_shifts[t]
            m = min((inst.shifts[i].cost / inst.shifts[i].duration
                     for i in covering), default=None)
            self.min_cost_per_minute.append(m)
            if inst.demand[t] > 0 and m is None:
                self.infeasible_reason = (
                    f"分钟 {t} 需求 {inst.demand[t]} 人，但无任何班次覆盖")
        self._zero_vector = (0,) * n

    # ---- 内部工具 ----

    def _coverage(self, counts):
        cov = [0] * WEEK_MINUTES
        for i, c in counts.items():
            if c:
                for t in self.inst.shifts[i].minutes:
                    cov[t] += c
        return cov

    def _residual(self, counts):
        cov = self._coverage(counts)
        d = self.inst.demand
        return [d[t] - cov[t] if d[t] > cov[t] else 0 for t in range(WEEK_MINUTES)]

    def _key(self, counts):
        cost = sum(self.inst.shifts[i].cost * c for i, c in counts.items())
        total = sum(counts.values())
        vector = tuple(counts.get(i, 0) for i in range(len(self.inst.shifts)))
        return (cost, total, vector)

    def _greedy(self, fixed):
        """从 fixed（不可变的已固定人数）出发贪心补全 + 反向删除，返回可行解或 None。"""
        inst = self.inst
        counts = dict(fixed)
        cov = self._coverage(counts)
        demand = inst.demand
        while True:
            best_t, best_r = -1, 0
            for t in range(WEEK_MINUTES):
                r = demand[t] - cov[t]
                if r > best_r:
                    best_r, best_t = r, t
            if best_r == 0:
                break
            best_i, best_key = -1, None
            for i in inst.minute_to_shifts[best_t]:
                if i in fixed:
                    continue
                sh = inst.shifts[i]
                gain = 0
                for t in sh.minutes:
                    if demand[t] > cov[t]:
                        gain += 1
                if gain == 0:
                    continue
                key = (sh.cost / gain, sh.sid)
                if best_key is None or key < best_key:
                    best_key, best_i = key, i
            if best_i < 0:
                return None
            counts[best_i] = counts.get(best_i, 0) + best_r
            for t in inst.shifts[best_i].minutes:
                cov[t] += best_r
        # 反向删除：按成本降序尝试减掉多余人数
        for i in sorted(counts, key=lambda i: (-inst.shifts[i].cost, inst.shifts[i].sid)):
            if i in fixed:
                continue
            minutes = inst.shifts[i].minutes
            while counts.get(i, 0) > 0 and all(
                    cov[t] - 1 >= demand[t] for t in minutes):
                counts[i] -= 1
                for t in minutes:
                    cov[t] -= 1
        return counts

    # ---- 主求解 ----

    def solve(self, max_nodes=20000, max_seconds=60.0):
        t0 = time.monotonic()
        deadline = t0 + max_seconds if max_seconds is not None else None
        inst = self.inst

        result = {
            "status": "unknown",
            "proven_optimal": False,
            "cost": None,
            "total_count": None,
            "assignment": None,
            "count_vector": None,
            "shift_order": [sh.sid for sh in inst.shifts],
            "cost_lower_bound": None,
            "cost_upper_bound": None,
            "nodes_expanded": 0,
            "budget_exhausted": False,
            "elapsed_seconds": None,
            "note": None,
        }
        if self.infeasible_reason is not None:
            result.update(status="infeasible", note=self.infeasible_reason,
                          elapsed_seconds=round(time.monotonic() - t0, 6))
            return result

        best_key = None      # (cost, total, vector)，小者为优
        best_counts = None

        def consider(counts):
            nonlocal best_key, best_counts
            key = self._key(counts)
            if best_key is None or key < best_key:
                best_key = key
                best_counts = {i: c for i, c in counts.items() if c}

        # 根节点贪心，先拿可行上界
        consider(self._greedy({}))

        m = self.min_cost_per_minute
        root_resid = list(inst.demand)
        root_dual = sum(root_resid[t] * m[t] for t in range(WEEK_MINUTES)
                        if root_resid[t])
        root_lb_total = max(root_resid) if root_resid else 0
        seq = itertools.count()
        # 堆元素: (lb_cost, lb_total, seq, fixed_cost, fixed_total, fixed)
        pq = [(root_dual, root_lb_total, next(seq), Fraction(0), 0, {})]
        nodes = 0
        exhausted = False
        proven = False

        while pq:
            if nodes >= max_nodes or (
                    deadline is not None and nodes % 64 == 0
                    and time.monotonic() > deadline):
                exhausted = True
                break
            lb_cost, lb_total, _, fixed_cost, fixed_total, fixed = heapq.heappop(pq)
            if best_key is not None and (lb_cost, lb_total) > (best_key[0], best_key[1]):
                # best-first：堆中最小下界已不可能改进最优键，其余节点同理
                proven = True
                break
            nodes += 1
            resid = self._residual(fixed)
            max_resid = max(resid)
            if max_resid == 0:
                consider(fixed)
                continue
            # 选剩余需求最大的分钟（取第一个）
            t_star = resid.index(max_resid)
            free_here = [i for i in inst.minute_to_shifts[t_star] if i not in fixed]
            if not free_here:
                continue  # 该节点不可行
            bi = min(free_here,
                     key=lambda i: (inst.shifts[i].cost / inst.shifts[i].duration,
                                    inst.shifts[i].sid))
            sh = inst.shifts[bi]
            minutes = sh.minutes
            mset = set(minutes)
            r_cap = max(resid[t] for t in minutes)  # 超过此值必被支配
            max_outside = 0
            dual_sum = Fraction(0)
            for t in range(WEEK_MINUTES):
                rt = resid[t]
                if rt:
                    dual_sum += rt * m[t]
                    if t not in mset and rt > max_outside:
                        max_outside = rt
            for j in range(r_cap + 1):
                child_fixed = dict(fixed)
                child_fixed[bi] = j
                child_fixed_cost = fixed_cost + j * sh.cost
                cut = Fraction(0)
                inside_max = 0
                for t in minutes:
                    rt = resid[t]
                    if rt:
                        cut += (rt if rt < j else j) * m[t]
                        rem = rt - j
                        if rem > inside_max:
                            inside_max = rem
                child_lb_cost = child_fixed_cost + dual_sum - cut
                child_lb_total = fixed_total + j + (
                    inside_max if inside_max > max_outside else max_outside)
                heapq.heappush(pq, (child_lb_cost, child_lb_total, next(seq),
                                    child_fixed_cost, fixed_total + j, child_fixed))
            if nodes % 32 == 0:
                g = self._greedy(fixed)
                if g is not None:
                    consider(g)
        else:
            proven = True  # 队列耗尽，全部空间已探索

        if best_counts is None:
            result["status"] = "infeasible" if proven else "unknown"
            result["note"] = ("探索完毕且无可行解" if proven else
                              "预算耗尽且尚未找到可行解")
        else:
            vector = tuple(best_counts.get(i, 0)
                           for i in range(len(inst.shifts)))
            result.update(
                cost=_num(best_key[0]),
                total_count=best_key[1],
                assignment={inst.shifts[i].sid: best_counts[i]
                            for i in sorted(best_counts,
                                            key=lambda i: inst.shifts[i].sid)},
                count_vector=list(vector),
                cost_upper_bound=_num(best_key[0]),
            )
            if proven:
                result["status"] = "optimal"
                result["proven_optimal"] = True
                result["cost_lower_bound"] = _num(best_key[0])
                result["note"] = "分支限界树已完全探索，解被证明最优"
            else:
                result["status"] = "feasible"
                lb = pq[0][0] if pq else best_key[0]
                if lb > best_key[0]:
                    lb = best_key[0]
                result["cost_lower_bound"] = _num(lb)
                result["note"] = (
                    "预算耗尽，未证明最优：成本下界 %s <= 最优值 <= 上界 %s"
                    % (_num(lb), _num(best_key[0])))
        result["nodes_expanded"] = nodes
        result["budget_exhausted"] = exhausted
        result["elapsed_seconds"] = round(time.monotonic() - t0, 6)
        return result


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="shift_cover",
        description="跨日班次最小人员覆盖求解器（分支限界，仅标准库）")
    parser.add_argument("input", nargs="?",
                        help="JSON 实例文件路径，缺省从标准输入读取")
    parser.add_argument("--max-nodes", type=int, default=20000,
                        help="分支限界节点预算（默认 20000）")
    parser.add_argument("--max-seconds", type=float, default=60.0,
                        help="时间预算秒数（默认 60）")
    parser.add_argument("--pretty", action="store_true", help="美化输出 JSON")
    args = parser.parse_args(argv)

    try:
        raw = open(args.input, encoding="utf-8").read() if args.input \
            else sys.stdin.read()
        inst = parse_instance(json.loads(raw))
    except (InputError, json.JSONDecodeError, OSError) as e:
        print(json.dumps({"status": "error", "error": str(e)},
                         ensure_ascii=False), file=sys.stderr)
        return 2

    result = Solver(inst).solve(max_nodes=args.max_nodes,
                                max_seconds=args.max_seconds)
    json.dump(result, sys.stdout, ensure_ascii=False,
              indent=2 if args.pretty else None)
    print()
    return 0 if result["status"] in ("optimal", "feasible") else 1


if __name__ == "__main__":
    sys.exit(main())
