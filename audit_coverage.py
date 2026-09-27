"""审计抽样覆盖优化。

问题定义
--------
给定一组风险场景（scenario），每个场景具有权重 weight，并要求至少 k 个
来自不同检查家族（family）的证据才算"充分覆盖"。给定一组检查（check），
每个检查属于一个家族，具有成本 cost 与执行工时 hours，并能为一组场景
提供证据。在总成本不超过 budget（可选：总工时不超过 hours_budget）的
约束下选择检查子集，按以下字典序目标优化：

  1. 最大化已充分覆盖场景的权重之和；
  2. 在前者相同的前提下，最小化总成本；
  3. 在前两者相同的前提下，所选检查 ID 排序后的元组字典序最小。

同一家族的多个检查对同一场景只贡献一个证据家族，不能虚增覆盖次数。

求解方法
--------
精确分支定界（branch and bound）枚举检查子集。上界估计：当前已覆盖权重
加上所有"仍有可能被充分覆盖"（当前证据家族并上剩余检查可提供的家族后
家族数可达 k）的场景权重。该上界是乐观可纳的，因此剪枝安全，返回结果
为精确最优解。问题本身为 NP-hard（预算约束最大覆盖的推广），求解时间
随检查数量指数增长，适用于中小规模实例。
"""

from __future__ import annotations

import json
import sys


def _validate(scenarios, checks):
    scen_ids = set()
    for s in scenarios:
        sid = s["id"]
        if sid in scen_ids:
            raise ValueError(f"重复的场景 ID: {sid!r}")
        scen_ids.add(sid)
        if s.get("k", 1) < 1:
            raise ValueError(f"场景 {sid!r} 的 k 必须 >= 1")
        if s.get("weight", 1) < 0:
            raise ValueError(f"场景 {sid!r} 的 weight 必须 >= 0")
    check_ids = set()
    for c in checks:
        cid = c["id"]
        if cid in check_ids:
            raise ValueError(f"重复的检查 ID: {cid!r}")
        check_ids.add(cid)
        if c["cost"] < 0 or c.get("hours", 0) < 0:
            raise ValueError(f"检查 {cid!r} 的 cost/hours 必须 >= 0")
        for sid in c.get("covers", []):
            if sid not in scen_ids:
                raise ValueError(f"检查 {cid!r} 覆盖了未知场景: {sid!r}")


def solve(scenarios, checks, budget, hours_budget=None):
    """精确求解，返回结果字典（结构见 README / main 的输出）。"""
    _validate(scenarios, checks)

    scen_ids = [s["id"] for s in scenarios]
    k_req = {s["id"]: s.get("k", 1) for s in scenarios}
    weight = {s["id"]: s.get("weight", 1) for s in scenarios}
    covers = [set(c.get("covers", [])) for c in checks]
    n = len(checks)

    # rem[i][sid]：checks[i:] 中能为场景 sid 提供证据的家族集合（用于上界）。
    rem = [None] * (n + 1)
    rem[n] = {sid: frozenset() for sid in scen_ids}
    for i in range(n - 1, -1, -1):
        fam = checks[i]["family"]
        cur = dict(rem[i + 1])
        for sid in covers[i]:
            cur[sid] = cur[sid] | {fam}
        rem[i] = cur

    sys.setrecursionlimit(max(sys.getrecursionlimit(), 4 * n + 1000))

    best_weight = 0
    best_cost = 0
    best_sel = ()  # 排序后的检查 ID 元组

    selected = []  # 当前已选检查 ID（未排序）
    fam_sets = {sid: set() for sid in scen_ids}  # 当前每个场景的证据家族

    def is_better(w, cost, sel_tuple):
        nonlocal best_weight, best_cost, best_sel
        if (w, -cost) != (best_weight, -best_cost):
            return (w, -cost) > (best_weight, -best_cost)
        return sel_tuple < best_sel

    def dfs(i, cost, hours, covered_weight):
        nonlocal best_weight, best_cost, best_sel
        if is_better(covered_weight, cost, tuple(sorted(selected))):
            best_weight, best_cost = covered_weight, cost
            best_sel = tuple(sorted(selected))
        if i == n:
            return
        # 上界剪枝：仍可能被充分覆盖的场景权重之和。
        bound = covered_weight
        for sid in scen_ids:
            if len(fam_sets[sid]) < k_req[sid]:
                if len(fam_sets[sid] | rem[i][sid]) >= k_req[sid]:
                    bound += weight[sid]
        if bound < best_weight:
            return
        c = checks[i]
        # 分支一：选择检查 i（若预算/工时允许）。
        if cost + c["cost"] <= budget and (
            hours_budget is None or hours + c.get("hours", 0) <= hours_budget
        ):
            fam = c["family"]
            added = []
            gained = 0
            for sid in covers[i]:
                if fam not in fam_sets[sid]:
                    fam_sets[sid].add(fam)
                    added.append(sid)
                    if len(fam_sets[sid]) == k_req[sid]:
                        gained += weight[sid]
            selected.append(c["id"])
            dfs(i + 1, cost + c["cost"], hours + c.get("hours", 0),
                covered_weight + gained)
            selected.pop()
            for sid in added:
                fam_sets[sid].discard(fam)
        # 分支二：不选检查 i。
        dfs(i + 1, cost, hours, covered_weight)

    dfs(0, 0, 0, 0)

    # 由最优选择重建输出。
    chosen = [c for c in checks if c["id"] in set(best_sel)]
    total_hours = sum(c.get("hours", 0) for c in chosen)
    scen_families = {sid: set() for sid in scen_ids}
    cover_map = {c["id"]: set(c.get("covers", [])) for c in checks}
    for c in chosen:
        for sid in cover_map[c["id"]]:
            scen_families[sid].add(c["family"])

    return {
        "selected_checks": list(best_sel),
        "total_cost": best_cost,
        "total_hours": total_hours,
        "covered_weight": best_weight,
        "total_weight": sum(weight.values()),
        "scenarios": {
            sid: {
                "covered": len(scen_families[sid]) >= k_req[sid],
                "required_families": k_req[sid],
                "evidence_families": sorted(scen_families[sid]),
            }
            for sid in scen_ids
        },
    }


def solve_payload(data):
    """从输入 JSON 字典求解。"""
    return solve(
        scenarios=data["scenarios"],
        checks=data["checks"],
        budget=data["budget"],
        hours_budget=data.get("hours_budget"),
    )


def main(argv=None):
    argv = list(sys.argv[1:] if argv is None else argv)
    if argv and argv[0] in ("-h", "--help"):
        print(__doc__)
        print("用法: python3 audit_coverage.py [input.json]  (缺省或 '-' 表示标准输入)")
        return 0
    if argv and argv[0] != "-":
        with open(argv[0], "r", encoding="utf-8") as f:
            data = json.load(f)
    else:
        data = json.load(sys.stdin)
    result = solve_payload(data)
    json.dump(result, sys.stdout, ensure_ascii=False, indent=2, sort_keys=True)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
