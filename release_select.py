#!/usr/bin/env python3.11
"""最小差异发布选择器.

问题模型:
  一次发布由若干变更(change)组成, 每个变更带有整数性能收益 benefit
  (可为负) 与整数资源消耗 cost(非负). 变更之间存在两类约束:
    * 依赖(deps): 选取某变更时必须同时选取其全部传递依赖;
      依赖环上的变更构成强连通分量, 必须整体选取或整体放弃(不视为错误);
    * 互斥(mutex): 互斥对中的两个变更不得同时选取(对称关系).
  选取集合的总 cost 不得超过整数容量 capacity.

目标:
  最大化总收益; 平局时变更数量最少; 仍平局时按排序后 ID 元组字典序最小.

输出:
  选取结果、约束满足证据(容量/依赖/互斥)以及精确穷举核验器的核验结论.
  当单元数超过穷举上限或用户跳过核验时, 明确报告"未验证", 不谎称最优.

仅使用 Python 3.11 标准库.
"""
from __future__ import annotations

import argparse
import json
import sys
from dataclasses import dataclass, field


# ---------------------------------------------------------------- 数据模型

@dataclass(frozen=True)
class Change:
    cid: str
    benefit: int
    cost: int
    deps: tuple = ()
    mutex: tuple = ()


@dataclass
class Unit:
    """依赖图的强连通分量: 必须整体选取或整体放弃."""

    index: int
    members: tuple          # 排序后的变更 ID 元组
    benefit: int
    cost: int
    deps: set = field(default_factory=set)        # 直接依赖的其他单元下标
    conflicts: set = field(default_factory=set)   # 互斥的其他单元下标
    internal_conflict: bool = False               # 分量内部存在互斥对 -> 不可选


# ---------------------------------------------------------------- 输入解析

def _as_int(value, what):
    if isinstance(value, bool) or not isinstance(value, int):
        raise ValueError(f"{what} 必须为整数, 得到: {value!r}")
    return value


def parse_instance(data):
    """解析并校验输入 JSON, 返回 (changes, capacity)."""
    if not isinstance(data, dict):
        raise ValueError("输入必须是 JSON 对象")
    capacity = _as_int(data.get("capacity"), "capacity")
    if capacity < 0:
        raise ValueError("capacity 必须为非负整数")
    raw = data.get("changes")
    if not isinstance(raw, list):
        raise ValueError("changes 必须为数组")
    changes = []
    seen = set()
    for i, item in enumerate(raw):
        if not isinstance(item, dict):
            raise ValueError(f"changes[{i}] 必须为对象")
        cid = item.get("id")
        if not isinstance(cid, str) or not cid:
            raise ValueError(f"changes[{i}].id 必须为非空字符串")
        if cid in seen:
            raise ValueError(f"变更 ID 重复: {cid!r}")
        seen.add(cid)
        benefit = _as_int(item.get("benefit"), f"变更 {cid} 的 benefit")
        cost = _as_int(item.get("cost"), f"变更 {cid} 的 cost")
        if cost < 0:
            raise ValueError(f"变更 {cid} 的 cost 必须为非负整数")
        deps = item.get("deps", [])
        mutex = item.get("mutex", [])
        for name, lst in (("deps", deps), ("mutex", mutex)):
            if not isinstance(lst, list) or not all(isinstance(x, str) for x in lst):
                raise ValueError(f"变更 {cid} 的 {name} 必须为字符串数组")
        changes.append(Change(cid, benefit, cost, tuple(deps), tuple(mutex)))
    for c in changes:
        for ref in c.deps + c.mutex:
            if ref not in seen:
                raise ValueError(f"变更 {c.cid} 引用了未知变更 {ref!r}")
            if ref == c.cid:
                raise ValueError(f"变更 {c.cid} 不得引用自身")
    return changes, capacity


# ---------------------------------------------------------------- 依赖环缩点

def tarjan_scc(nodes, adj):
    """Tarjan 强连通分量, 返回成员排序后的分量列表."""
    sys.setrecursionlimit(max(10000, len(nodes) * 4 + 100))
    index_of, lowlink, on_stack, stack, components = {}, {}, set(), [], []
    counter = 0

    def strongconnect(v):
        nonlocal counter
        index_of[v] = lowlink[v] = counter
        counter += 1
        stack.append(v)
        on_stack.add(v)
        for w in adj[v]:
            if w not in index_of:
                strongconnect(w)
                lowlink[v] = min(lowlink[v], lowlink[w])
            elif w in on_stack:
                lowlink[v] = min(lowlink[v], index_of[w])
        if lowlink[v] == index_of[v]:
            comp = []
            while True:
                w = stack.pop()
                on_stack.discard(w)
                comp.append(w)
                if w == v:
                    break
            components.append(tuple(sorted(comp)))

    for v in nodes:
        if v not in index_of:
            strongconnect(v)
    return components


def build_units(changes):
    """把变更图缩成单元(强连通分量) DAG, 返回 (units, unit_of, mutex_pairs)."""
    by_id = {c.cid: c for c in changes}
    adj = {c.cid: set(c.deps) for c in changes}
    components = tarjan_scc(list(by_id), adj)
    units, unit_of = [], {}
    for idx, members in enumerate(components):
        units.append(Unit(
            index=idx,
            members=members,
            benefit=sum(by_id[m].benefit for m in members),
            cost=sum(by_id[m].cost for m in members),
        ))
        for m in members:
            unit_of[m] = idx
    for c in changes:
        for d in c.deps:
            if unit_of[d] != unit_of[c.cid]:
                units[unit_of[c.cid]].deps.add(unit_of[d])
    pairs = set()
    for c in changes:
        for m in c.mutex:
            pairs.add(tuple(sorted((c.cid, m))))
    for a, b in sorted(pairs):
        ua, ub = unit_of[a], unit_of[b]
        if ua == ub:
            units[ua].internal_conflict = True
        else:
            units[ua].conflicts.add(ub)
            units[ub].conflicts.add(ua)
    return units, unit_of, sorted(pairs)


def compute_closures(units):
    """每个单元的依赖闭包(含自身), 缩点后为 DAG, 递归安全."""
    memo = {}

    def clo(u):
        if u not in memo:
            result = {u}
            for d in units[u].deps:
                result |= clo(d)
            memo[u] = frozenset(result)
        return memo[u]

    return [clo(u.index) for u in units]


def compute_selectable(units, closures, capacity):
    """单元可选性预检: 闭包内无内部互斥、无互斥对、闭包成本不超容量."""
    selectable = []
    for u in units:
        clo = closures[u.index]
        ok = not any(units[v].internal_conflict for v in clo)
        ok = ok and not any(units[v].conflicts & clo for v in clo)
        ok = ok and sum(units[v].cost for v in clo) <= capacity
        selectable.append(ok)
    return selectable


# ---------------------------------------------------------------- 目标比较

def _better(cand, best):
    """cand/best 为 (benefit, ids_tuple); 收益高者优先, 平量少者优先, 再平字典序小者优先."""
    if cand[0] != best[0]:
        return cand[0] > best[0]
    if len(cand[1]) != len(best[1]):
        return len(cand[1]) < len(best[1])
    return cand[1] < best[1]


# ---------------------------------------------------------------- 精确求解

def optimize(units, closures, selectable, capacity):
    """分支限界精确求解. 返回 {'benefit','cost','ids'}."""
    order = [u.index for u in units if selectable[u.index]]
    suffix_pos = [0] * (len(order) + 1)
    for i in range(len(order) - 1, -1, -1):
        suffix_pos[i] = suffix_pos[i + 1] + max(0, units[order[i]].benefit)
    best = {"benefit": 0, "cost": 0, "ids": ()}  # 空集恒可行

    def dfs(pos, selected, benefit, cost):
        if benefit + suffix_pos[pos] < best["benefit"]:
            return  # 上界剪枝: 剩余正收益全拿也无法反超
        if pos == len(order):
            ids = tuple(sorted(m for u in selected for m in units[u].members))
            if _better((benefit, ids), (best["benefit"], best["ids"])):
                best.update(benefit=benefit, cost=cost, ids=ids)
            return
        dfs(pos + 1, selected, benefit, cost)  # 不选 order[pos]
        u = order[pos]
        add = closures[u] - selected
        add_cost = sum(units[a].cost for a in add)
        if cost + add_cost > capacity:
            return
        if any(units[a].conflicts & selected for a in add):
            return
        add_benefit = sum(units[a].benefit for a in add)
        dfs(pos + 1, selected | add, benefit + add_benefit, cost + add_cost)

    dfs(0, frozenset(), 0, 0)
    return best


# ---------------------------------------------------------------- 穷举核验器

def evaluate_selection(units, closures, selected, capacity):
    """检查一个单元集合是否可行; 可行返回 {'benefit','cost','ids'}, 否则 None."""
    for u in selected:
        if units[u].internal_conflict or not closures[u] <= selected:
            return None
    for u in selected:
        if units[u].conflicts & selected:
            return None
    cost = sum(units[u].cost for u in selected)
    if cost > capacity:
        return None
    return {
        "benefit": sum(units[u].benefit for u in selected),
        "cost": cost,
        "ids": tuple(sorted(m for u in selected for m in units[u].members)),
    }


def verify_exhaustive(units, closures, capacity, limit=20):
    """对全部 2^n 个单元子集逐一判定可行性并取最优, 与求解器结果对照."""
    n = len(units)
    if n > limit:
        return {
            "verified": False,
            "reason": f"单元数 {n} 超过穷举上限 {limit}, 未执行穷举, 最优性未验证",
            "units": n,
            "limit": limit,
        }
    best, feasible = None, 0
    for mask in range(1 << n):
        selected = frozenset(i for i in range(n) if mask >> i & 1)
        ev = evaluate_selection(units, closures, selected, capacity)
        if ev is None:
            continue
        feasible += 1
        if best is None or _better((ev["benefit"], ev["ids"]), (best["benefit"], best["ids"])):
            best = ev
    return {
        "verified": True,
        "units": n,
        "enumerated_sets": 1 << n,
        "feasible_sets": feasible,
        "best": {"benefit": best["benefit"], "cost": best["cost"], "ids": list(best["ids"])},
    }


# ---------------------------------------------------------------- 证据与报告

def build_evidence(by_id, units, mutex_pairs, selected_ids, capacity):
    sel = set(selected_ids)
    dep_ev = [
        {"change": cid, "requires": d, "satisfied": d in sel}
        for cid in sorted(sel) for d in sorted(by_id[cid].deps)
    ]
    mutex_ev = [
        {"pair": [a, b], "both_selected": a in sel and b in sel,
         "satisfied": not (a in sel and b in sel)}
        for a, b in mutex_pairs
    ]
    used = sum(by_id[c].cost for c in sel)
    cap_ev = {"used": used, "capacity": capacity, "satisfied": used <= capacity}
    groups = [
        {"members": list(u.members), "benefit": u.benefit, "cost": u.cost,
         "forced_together": len(u.members) > 1,
         "internal_mutex_conflict": u.internal_conflict,
         "selected": all(m in sel for m in u.members)}
        for u in units
    ]
    all_ok = (all(e["satisfied"] for e in dep_ev)
              and all(e["satisfied"] for e in mutex_ev)
              and cap_ev["satisfied"])
    evidence = {
        "capacity": cap_ev,
        "dependencies": dep_ev,
        "mutex": mutex_ev,
        "all_constraints_satisfied": all_ok,
    }
    return evidence, groups


def build_report(data, verify=True, verify_limit=20):
    changes, capacity = parse_instance(data)
    by_id = {c.cid: c for c in changes}
    units, unit_of, mutex_pairs = build_units(changes)
    closures = compute_closures(units)
    selectable = compute_selectable(units, closures, capacity)
    best = optimize(units, closures, selectable, capacity)
    evidence, groups = build_evidence(by_id, units, mutex_pairs, best["ids"], capacity)
    report = {
        "selection": {
            "ids": list(best["ids"]),
            "benefit": best["benefit"],
            "cost": best["cost"],
            "change_count": len(best["ids"]),
        },
        "objective": "最大化总收益; 平局时变更数最少; 再平局时排序后 ID 字典序最小",
        "groups": groups,
        "evidence": evidence,
    }
    if verify:
        vres = verify_exhaustive(units, closures, capacity, limit=verify_limit)
        if vres["verified"]:
            matches = tuple(vres["best"]["ids"]) == best["ids"]
            vres["matches_solver"] = matches
            vres["optimality_proven"] = matches
        report["verification"] = vres
    else:
        report["verification"] = {
            "verified": False,
            "reason": "已通过 --no-verify 跳过穷举核验, 最优性未验证",
        }
    return report


# ---------------------------------------------------------------- CLI

def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="release_select",
        description="最小差异发布选择: 依赖(环强制同选)/互斥/容量约束下的最大收益变更集",
    )
    parser.add_argument("input", help="输入 JSON 文件路径, '-' 表示标准输入")
    parser.add_argument("--no-verify", action="store_true", help="跳过精确穷举核验")
    parser.add_argument("--verify-limit", type=int, default=20,
                        help="穷举核验的单元数上限(默认 20)")
    parser.add_argument("--compact", action="store_true", help="紧凑 JSON 输出")
    args = parser.parse_args(argv)
    try:
        with (sys.stdin if args.input == "-" else open(args.input, encoding="utf-8")) as fh:
            data = json.load(fh)
        report = build_report(data, verify=not args.no_verify, verify_limit=args.verify_limit)
    except (ValueError, KeyError, json.JSONDecodeError, OSError) as exc:
        print(f"错误: {exc}", file=sys.stderr)
        return 2
    json.dump(report, sys.stdout, ensure_ascii=False,
              indent=None if args.compact else 2)
    print()
    verification = report["verification"]
    if verification.get("verified") and not verification.get("matches_solver", False):
        print("错误: 穷举核验与求解器结果不一致", file=sys.stderr)
        return 3
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
