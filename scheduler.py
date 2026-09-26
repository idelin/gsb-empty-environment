#!/usr/bin/env python3
"""单机工单排程（含维护窗口）。

工单：释放时间、截止时间、整数持续时长、权重、前置依赖。
机器：每时刻至多执行一个工单，存在不可用的维护窗口，工单不可抢占。
所有区间均为半开区间 [start, end)：端点接触不算冲突。

目标（字典序）：
  1. 最大化已完成工单的总权重；
  2. 在权重最大的前提下，最小化总完成时间。

求解器为精确算法（按拓扑序枚举 + 贪心最早开工），适用于中小规模算例；
另提供独立的暴力枚举核验器，可在小规模算例上交叉验证最优性。
"""
from __future__ import annotations

import argparse
import itertools
import json
import sys
from dataclasses import dataclass


# ---------------------------------------------------------------- 数据模型

@dataclass(frozen=True)
class Job:
    id: str
    release: int          # 释放时间（可开工的最早时刻）
    deadline: int         # 截止时间（完成时刻 <= deadline 即按时）
    duration: int         # 整数持续时长，>= 1
    weight: int           # 完成权重
    predecessors: tuple = ()  # 前置工单 id 元组


def overlaps(a_start: int, a_end: int, b_start: int, b_end: int) -> bool:
    """半开区间相交判定：端点接触（a_end == b_start）不算冲突。"""
    return a_start < b_end and b_start < a_end


def merge_windows(windows) -> list:
    """排序并合并维护窗口；拒绝 start > end 的非法窗口。"""
    cleaned = []
    for w in windows:
        a, b = int(w[0]), int(w[1])
        if b < a:
            raise ValueError(f"维护窗口非法：[{a}, {b})，结束早于开始")
        if b > a:
            cleaned.append((a, b))
    cleaned.sort()
    merged = []
    for a, b in cleaned:
        if merged and a <= merged[-1][1]:
            merged[-1][1] = max(merged[-1][1], b)
        else:
            merged.append([a, b])
    return [(a, b) for a, b in merged]


def earliest_start(t: int, duration: int, windows) -> int:
    """不早于 t 且避开所有维护窗口的最早开工时刻（半开区间）。"""
    s = t
    for a, b in windows:
        if b <= s:
            continue
        if a >= s + duration:
            break
        s = b  # [s, s+duration) 与 [a, b) 相交，跳到窗口结束
    return s


# ---------------------------------------------------------------- 输入校验

def parse_jobs(raw_jobs) -> list:
    jobs = []
    seen = set()
    for r in raw_jobs:
        jid = str(r["id"])
        if jid in seen:
            raise ValueError(f"工单 id 重复：{jid}")
        seen.add(jid)
        release = int(r.get("release", 0))
        deadline = int(r["deadline"])
        duration = int(r["duration"])
        weight = int(r.get("weight", 1))
        preds = tuple(str(p) for p in r.get("predecessors", []))
        if duration < 1:
            raise ValueError(f"工单 {jid} 持续时长必须 >= 1")
        if deadline < release:
            raise ValueError(f"工单 {jid} 截止时间早于释放时间")
        jobs.append(Job(jid, release, deadline, duration, weight, preds))
    ids = {j.id for j in jobs}
    for j in jobs:
        for p in j.predecessors:
            if p not in ids:
                raise ValueError(f"工单 {j.id} 的前置 {p} 不存在")
            if p == j.id:
                raise ValueError(f"工单 {j.id} 不能依赖自身")
    _check_acyclic(jobs)
    return jobs


def _check_acyclic(jobs) -> None:
    preds = {j.id: set(j.predecessors) for j in jobs}
    done = set()
    while len(done) < len(jobs):
        ready = [jid for jid, ps in preds.items()
                 if jid not in done and ps <= done]
        if not ready:
            cycle = sorted(set(preds) - done)
            raise ValueError(f"前置依赖存在环，涉及工单：{cycle}")
        done.update(ready)


# ---------------------------------------------------------------- 精确求解器

def solve(jobs, maintenance) -> dict:
    """精确求解。返回 {weight, completion, schedule: {id: (start, end)}}。

    枚举所有满足前置依赖的开工顺序；对固定顺序，逐单取最早可行开工时刻
    （该贪心对该顺序下每个工单的完成时刻都是最小的，因此不丢最优性）。
    每完成一个前缀即评估一次目标，等价于枚举所有可行子集。
    """
    windows = merge_windows(maintenance)
    jmap = {j.id: j for j in jobs}
    preds = {j.id: set(j.predecessors) for j in jobs}

    placed = set()
    sched = {}
    best = {"weight": 0, "completion": 0, "schedule": {}}

    def rec(free, weight, completion, remaining_positive):
        nonlocal best
        if (weight, -completion) > (best["weight"], -best["completion"]):
            best = {"weight": weight, "completion": completion,
                    "schedule": dict(sched)}
        if weight + remaining_positive < best["weight"]:
            return  # 剩余正权重全拿也无法超越当前最优
        for jid, j in jmap.items():
            if jid in placed or not preds[jid] <= placed:
                continue
            s = earliest_start(max(j.release, free), j.duration, windows)
            e = s + j.duration
            if e > j.deadline:
                continue  # 该顺序下此单已不可行（再往后放只会更晚）
            placed.add(jid)
            sched[jid] = (s, e)
            rec(e, weight + j.weight, completion + e,
                remaining_positive - max(0, j.weight))
            placed.discard(jid)
            del sched[jid]

    rec(0, 0, 0, sum(j.weight for j in jobs if j.weight > 0))
    return best


# ---------------------------------------------------------------- 独立暴力核验

def brute_force_best(jobs, maintenance, max_combos: int = 2_000_000):
    """独立暴力枚举：每个工单取「不排 / 每个可行开工时刻」，直接校验全部约束。

    返回 (best, total_combos)；组合数超限返回 (None, total_combos)。
    best = ((total_weight, total_completion), {id: start})
    """
    windows = merge_windows(maintenance)
    jmap = {j.id: j for j in jobs}
    options = []
    for j in jobs:
        starts = [None] + list(range(j.release, j.deadline - j.duration + 1))
        options.append(starts)
    total = 1
    for o in options:
        total *= len(o)
    if total > max_combos:
        return None, total

    best_key = (0, 0)  # (weight, -completion)
    best_assign = {}
    for combo in itertools.product(*options):
        assign = {j.id: s for j, s in zip(jobs, combo) if s is not None}
        if not _assignment_valid(assign, jmap, windows):
            continue
        weight = sum(jmap[i].weight for i in assign)
        completion = sum(s + jmap[i].duration for i, s in assign.items())
        if (weight, -completion) > best_key:
            best_key = (weight, -completion)
            best_assign = assign
    return ((best_key[0], -best_key[1]), best_assign), total


def _assignment_valid(assign, jmap, windows) -> bool:
    for jid, s in assign.items():
        j = jmap[jid]
        e = s + j.duration
        if s < j.release or e > j.deadline:
            return False
        for a, b in windows:
            if overlaps(s, e, a, b):
                return False
        for p in j.predecessors:
            if p not in assign or assign[p] + jmap[p].duration > s:
                return False
    items = sorted(assign.items(), key=lambda kv: kv[1])
    for (i1, s1), (i2, s2) in zip(items, items[1:]):
        if s1 + jmap[i1].duration > s2:  # 单机不重叠（接触允许）
            return False
    return True


def verify(jobs, maintenance, solution, max_combos: int = 2_000_000) -> dict:
    """用独立暴力枚举核验求解器结果的最优性（仅限小算例）。"""
    bf, total = brute_force_best(jobs, maintenance, max_combos)
    if bf is None:
        return {"status": "skipped",
                "reason": f"组合数 {total} 超过上限 {max_combos}，未核验",
                "combinations": total}
    (bf_weight, bf_completion), _ = bf
    ok = (solution["weight"] == bf_weight
          and solution["completion"] == bf_completion)
    return {
        "status": "optimal_verified" if ok else "mismatch",
        "combinations": total,
        "solver_objective": {"total_weight": solution["weight"],
                             "total_completion": solution["completion"]},
        "brute_force_objective": {"total_weight": bf_weight,
                                  "total_completion": bf_completion},
    }


# ---------------------------------------------------------------- 未安排原因

def _ancestors(jid, preds):
    out, stack = set(), [jid]
    while stack:
        cur = stack.pop()
        if cur in out:
            continue
        out.add(cur)
        stack.extend(preds.get(cur, ()))
    return out


def unscheduled_reasons(jobs, maintenance, scheduled_ids) -> list:
    windows = merge_windows(maintenance)
    jmap = {j.id: j for j in jobs}
    preds = {j.id: set(j.predecessors) for j in jobs}
    reasons = []
    for j in jobs:
        if j.id in scheduled_ids:
            continue
        # 1) 单看自身：释放-截止之间是否存在避开维护窗口的完整时段
        s = earliest_start(j.release, j.duration, windows)
        if s + j.duration > j.deadline:
            reasons.append({
                "id": j.id, "reason_code": "no_feasible_slot",
                "reason": "释放时间与截止时间之间不存在避开维护窗口的完整时段"})
            continue
        # 2) 前置依赖链（含自身）独占机器也无法全部按时完成
        closure = _ancestors(j.id, preds)
        sub_jobs = [jmap[i] for i in closure]
        sub = solve(sub_jobs, windows)
        if set(sub["schedule"]) != closure:
            reasons.append({
                "id": j.id, "reason_code": "infeasible_precedence_chain",
                "reason": "前置依赖链无法在各自时间窗内全部按时完成"})
            continue
        # 3) 本身可行，但被优化目标舍弃
        reasons.append({
            "id": j.id, "reason_code": "resource_contention",
            "reason": "与其它工单竞争机器资源，为最大化完成权重/最小化总完成时间而未安排"})
    return reasons


# ---------------------------------------------------------------- 顶层流程

def run(payload: dict, do_verify: bool = True,
        max_combos: int = 2_000_000) -> dict:
    jobs = parse_jobs(payload.get("jobs", []))
    maintenance = payload.get("maintenance", [])
    solution = solve(jobs, maintenance)
    schedule = [{"id": jid, "start": s, "end": e}
                for jid, (s, e) in sorted(solution["schedule"].items(),
                                          key=lambda kv: (kv[1][0], kv[0]))]
    scheduled_ids = set(solution["schedule"])
    result = {
        "objective": {"total_weight": solution["weight"],
                      "total_completion": solution["completion"]},
        "schedule": schedule,
        "unscheduled": unscheduled_reasons(jobs, maintenance, scheduled_ids),
    }
    if do_verify:
        result["verification"] = verify(jobs, maintenance, solution, max_combos)
    else:
        result["verification"] = {"status": "disabled",
                                  "reason": "已通过 --no-verify 关闭核验"}
    return result


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(
        description="单机工单排程（含维护窗口）：先最大化完成权重，再最小化总完成时间")
    ap.add_argument("input", nargs="?", default="-",
                    help="输入 JSON 文件路径，省略或 '-' 表示从标准输入读取")
    ap.add_argument("--no-verify", action="store_true",
                    help="跳过小算例最优性核验")
    ap.add_argument("--max-combos", type=int, default=2_000_000,
                    help="暴力核验允许的最大组合数（默认 2000000）")
    ap.add_argument("--compact", action="store_true",
                    help="输出紧凑 JSON（默认缩进 2 格）")
    args = ap.parse_args(argv)

    try:
        text = (sys.stdin.read() if args.input == "-"
                else open(args.input, encoding="utf-8").read())
        payload = json.loads(text)
        result = run(payload, do_verify=not args.no_verify,
                     max_combos=args.max_combos)
    except (ValueError, KeyError, json.JSONDecodeError) as exc:
        print(json.dumps({"error": str(exc)}, ensure_ascii=False),
              file=sys.stderr)
        return 2

    indent = None if args.compact else 2
    print(json.dumps(result, ensure_ascii=False, indent=indent))
    return 0


if __name__ == "__main__":
    sys.exit(main())
