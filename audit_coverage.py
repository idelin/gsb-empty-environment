#!/usr/bin/env python3
"""审计抽样覆盖优化。

给定风险场景集合与检查项集合：
- 每个检查项属于一个检查家族，有成本与执行工时，并覆盖若干风险场景；
- 每个风险场景要求至少 k 个来自**不同检查家族**的证据才算充分覆盖
  （同一家族的多个检查只计 1 个家族，不能虚增覆盖次数）；
- 在预算（成本预算，可选工时预算）约束下，按字典序优化目标：
    1. 最大化已充分覆盖场景的权重之和；
    2. 最小化所选检查的总成本；
    3. 最小化所选检查 ID 排序后的字典序。

求解器为带剪枝的分支定界穷举，对有限输入返回**可证明的最优解**
（剪枝只去除不可能优于当前最优的分支，不改变最优性）。

Python 3.11 标准库，无第三方依赖。
"""
from __future__ import annotations

import argparse
import json
import sys


def _validate(scenarios, checks, budget, hours_budget):
    if budget < 0:
        raise ValueError("budget 必须非负")
    if hours_budget is not None and hours_budget < 0:
        raise ValueError("hours_budget 必须非负")
    scen_ids = [s["id"] for s in scenarios]
    if len(set(scen_ids)) != len(scen_ids):
        raise ValueError("场景 id 存在重复")
    check_ids = [c["id"] for c in checks]
    if len(set(check_ids)) != len(check_ids):
        raise ValueError("检查 id 存在重复")
    scen_set = set(scen_ids)
    for s in scenarios:
        if s.get("k", 1) < 1:
            raise ValueError(f"场景 {s['id']} 的 k 必须 >= 1")
        if s.get("weight", 1) < 0:
            raise ValueError(f"场景 {s['id']} 的 weight 必须非负")
    for c in checks:
        if "family" not in c:
            raise ValueError(f"检查 {c['id']} 缺少 family")
        if c.get("cost", 0) < 0 or c.get("hours", 0) < 0:
            raise ValueError(f"检查 {c['id']} 的 cost/hours 必须非负")
        unknown = set(c.get("covers", [])) - scen_set
        if unknown:
            raise ValueError(f"检查 {c['id']} 覆盖了未知场景: {sorted(unknown)}")


def solve(scenarios, checks, budget, hours_budget=None):
    """精确求解，返回结果字典（结构见 README / CLI 输出）。"""
    _validate(scenarios, checks, budget, hours_budget)

    scen_ids = [s["id"] for s in scenarios]
    m = len(scenarios)
    sidx = {sid: j for j, sid in enumerate(scen_ids)}
    k_req = [s.get("k", 1) for s in scenarios]
    weight = [s.get("weight", 1) for s in scenarios]

    # 按检查 ID 排序，保证枚举顺序确定、选中 ID 列表天然有序
    checks = sorted(checks, key=lambda c: c["id"])
    n = len(checks)
    cid = [c["id"] for c in checks]
    fam = [c["family"] for c in checks]
    cost = [c.get("cost", 0) for c in checks]
    hrs = [c.get("hours", 0) for c in checks]
    covers = [set(c.get("covers", [])) for c in checks]

    # suffix_fams[i][j]：checks[i:] 中覆盖场景 j 的家族集合（用于上界剪枝）
    suffix_fams = [[frozenset() for _ in range(m)] for _ in range(n + 1)]
    for i in range(n - 1, -1, -1):
        row = [suffix_fams[i + 1][j] for j in range(m)]
        for sid in covers[i]:
            j = sidx[sid]
            row[j] = row[j] | {fam[i]}
        suffix_fams[i] = row

    cur_fams = [set() for _ in range(m)]  # 当前已收集的家族证据
    cur_ids = []
    best = {"weight": -1, "cost": 0, "ids": None, "fams": None}

    def better(w, c, ids):
        bw, bc, bids = best["weight"], best["cost"], best["ids"]
        if bids is None:
            return True
        if w != bw:
            return w > bw
        if c != bc:
            return c < bc
        return ids < bids

    def dfs(i, cur_cost, cur_hours, cur_weight):
        # 预算剪枝（成本/工时均非负，超限后不可能回到限内）
        if cur_cost > budget:
            return
        if hours_budget is not None and cur_hours > hours_budget:
            return

        # 上界剪枝：未覆盖场景只有在剩余检查仍能补足家族缺口时才可能覆盖
        ub = cur_weight
        for j in range(m):
            if len(cur_fams[j]) >= k_req[j]:
                continue
            if len(cur_fams[j] | suffix_fams[i][j]) >= k_req[j]:
                ub += weight[j]
        if ub < best["weight"]:
            return
        if ub == best["weight"] and cur_cost > best["cost"]:
            return

        # 每个节点对应一个可行子集，尝试更新最优
        if better(cur_weight, cur_cost, tuple(cur_ids)):
            best["weight"] = cur_weight
            best["cost"] = cur_cost
            best["ids"] = tuple(cur_ids)
            best["fams"] = [frozenset(fs) for fs in cur_fams]

        if i == n:
            return

        # 分支 1：选入检查 i
        touched = []
        for sid in covers[i]:
            j = sidx[sid]
            if fam[i] not in cur_fams[j]:
                cur_fams[j].add(fam[i])
                touched.append(j)
        gained = sum(weight[j] for j in set(touched) if len(cur_fams[j]) == k_req[j])
        cur_ids.append(cid[i])
        dfs(i + 1, cur_cost + cost[i], cur_hours + hrs[i], cur_weight + gained)
        cur_ids.pop()
        for j in touched:
            cur_fams[j].discard(fam[i])

        # 分支 2：不选检查 i
        dfs(i + 1, cur_cost, cur_hours, cur_weight)

    dfs(0, 0, 0, 0)

    selected = list(best["ids"])
    sel_set = set(selected)
    total_hours = sum(c.get("hours", 0) for c in checks if c["id"] in sel_set)
    scen_out = []
    for j, sid in enumerate(scen_ids):
        families = sorted(best["fams"][j])
        scen_out.append({
            "id": sid,
            "k_required": k_req[j],
            "covered": len(families) >= k_req[j],
            "family_count": len(families),
            "families": families,
        })
    return {
        "objective": {
            "covered_weight": best["weight"],
            "total_cost": best["cost"],
            "total_hours": total_hours,
        },
        "selected_checks": selected,
        "scenarios": scen_out,
    }


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="审计抽样覆盖优化（精确求解，字典序多目标）")
    parser.add_argument("input", nargs="?", default=None,
                        help="输入 JSON 文件；缺省则从标准输入读取")
    parser.add_argument("-o", "--output", default=None,
                        help="输出 JSON 文件；缺省打印到标准输出")
    args = parser.parse_args(argv)

    try:
        if args.input:
            with open(args.input, "r", encoding="utf-8") as fh:
                data = json.load(fh)
        else:
            data = json.load(sys.stdin)
    except (OSError, json.JSONDecodeError) as exc:
        print(f"读取输入失败: {exc}", file=sys.stderr)
        return 2

    try:
        result = solve(
            scenarios=data.get("scenarios", []),
            checks=data.get("checks", []),
            budget=data["budget"],
            hours_budget=data.get("hours_budget"),
        )
    except (KeyError, ValueError, TypeError) as exc:
        print(f"输入不合法: {exc}", file=sys.stderr)
        return 2

    text = json.dumps(result, ensure_ascii=False, indent=2)
    if args.output:
        with open(args.output, "w", encoding="utf-8") as fh:
            fh.write(text + "\n")
    else:
        print(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
