"""容量约束证据打包（capacity-constrained evidence packing）。

给定一组证据文件（大小、价值、依赖文件集合、互斥版本组）与容量上限，
选择一个交付包子集，使得：
  * 总大小（重复依赖只计一次，即所选集合并集大小之和）不超过容量；
  * 依赖闭包完整：选中任何文件，其全部（传递）依赖也必须被选中；
  * 互斥版本组内至多选择一个文件；
在上述约束下最大化总价值；平局时依次取更小总大小、ID 字典序更小者。

搜索为精确分支限界。节点预算内完成全树（或经有效上界剪枝）搜索时，
结果标记为 proven_optimal（已证最优）；预算耗尽提前终止时，仅报告
best_known（最佳已知），并明确 proven_optimal = false。

仅使用 Python 3.11 标准库。
"""

from __future__ import annotations

import argparse
import json
import sys
from dataclasses import dataclass


# ---------------------------------------------------------------------------
# 数据模型与输入校验
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Item:
    id: str
    size: int
    value: int
    dependencies: tuple[str, ...] = ()
    mutex_group: str | None = None


class InputError(ValueError):
    """输入数据非法。"""


def parse_items(data: object) -> list[Item]:
    if not isinstance(data, list):
        raise InputError("'items' 必须是数组")
    items: list[Item] = []
    seen: set[str] = set()
    for raw in data:
        if not isinstance(raw, dict):
            raise InputError("每个 item 必须是对象")
        try:
            iid = raw["id"]
            size = raw["size"]
            value = raw["value"]
        except KeyError as exc:
            raise InputError(f"item 缺少必填字段: {exc}") from exc
        if not isinstance(iid, str) or not iid:
            raise InputError(f"非法 id: {iid!r}")
        if iid in seen:
            raise InputError(f"重复的 id: {iid!r}")
        seen.add(iid)
        if not isinstance(size, int) or isinstance(size, bool) or size < 0:
            raise InputError(f"item {iid!r}: size 必须是非负整数")
        if not isinstance(value, int) or isinstance(value, bool):
            raise InputError(f"item {iid!r}: value 必须是整数")
        deps = raw.get("dependencies", [])
        if not isinstance(deps, list) or not all(isinstance(d, str) for d in deps):
            raise InputError(f"item {iid!r}: dependencies 必须是字符串数组")
        if len(set(deps)) != len(deps):
            raise InputError(f"item {iid!r}: dependencies 含重复项")
        if iid in deps:
            raise InputError(f"item {iid!r}: 不允许自依赖")
        group = raw.get("mutex_group")
        if group is not None and not isinstance(group, str):
            raise InputError(f"item {iid!r}: mutex_group 必须是字符串或 null")
        items.append(Item(iid, size, value, tuple(deps), group))
    by_id = {it.id: it for it in items}
    for it in items:
        for dep in it.dependencies:
            if dep not in by_id:
                raise InputError(f"item {it.id!r}: 依赖了不存在的文件 {dep!r}")
    return items


def parse_instance(payload: object) -> tuple[list[Item], int, int | None]:
    if not isinstance(payload, dict):
        raise InputError("输入必须是 JSON 对象")
    if "capacity" not in payload or "items" not in payload:
        raise InputError("输入必须包含 'capacity' 与 'items'")
    capacity = payload["capacity"]
    if not isinstance(capacity, int) or isinstance(capacity, bool) or capacity < 0:
        raise InputError("capacity 必须是非负整数")
    items = parse_items(payload["items"])
    max_nodes = payload.get("search", {}).get("max_nodes")
    if max_nodes is not None:
        if not isinstance(max_nodes, int) or isinstance(max_nodes, bool) or max_nodes < 1:
            raise InputError("search.max_nodes 必须是正整数")
    return items, capacity, max_nodes


# ---------------------------------------------------------------------------
# 依赖闭包与拓扑
# ---------------------------------------------------------------------------

def dependency_closure(iid: str, by_id: dict[str, Item]) -> frozenset[str]:
    """iid 的传递依赖闭包（含 iid 自身）。对依赖环安全。"""
    result: set[str] = set()
    stack = [iid]
    while stack:
        cur = stack.pop()
        if cur in result:
            continue
        result.add(cur)
        stack.extend(by_id[cur].dependencies)
    return frozenset(result)


def topological_order(selected: set[str], by_id: dict[str, Item]) -> tuple[list[str], bool]:
    """对所选子图做拓扑排序（依赖先于依赖者）。返回 (顺序, 是否无环)。"""
    order: list[str] = []
    state: dict[str, int] = {}  # 1=在栈中, 2=完成
    acyclic = True

    def visit(u: str) -> None:
        nonlocal acyclic
        state[u] = 1
        for v in sorted(by_id[u].dependencies):
            if v not in selected:
                continue
            s = state.get(v, 0)
            if s == 1:
                acyclic = False
                continue
            if s == 0:
                visit(v)
        state[u] = 2
        order.append(u)

    for u in sorted(selected):
        if state.get(u, 0) == 0:
            visit(u)
    return order, acyclic


# ---------------------------------------------------------------------------
# 精确分支限界求解器
# ---------------------------------------------------------------------------

class _BudgetExhausted(Exception):
    pass


def solve(items: list[Item], capacity: int, max_nodes: int | None = None) -> dict:
    """返回 {ids, total_value, total_size, proven_optimal, nodes_explored, budget_exhausted}。"""
    by_id = {it.id: it for it in items}
    order = sorted(by_id)
    n = len(order)
    closures = {iid: dependency_closure(iid, by_id) for iid in order}
    # 后缀正价值和：用于值上界剪枝（对未决定项取正价值之和，是可达价值的有效上界）。
    suffix_pos = [0] * (n + 1)
    for i in range(n - 1, -1, -1):
        suffix_pos[i] = suffix_pos[i + 1] + max(0, by_id[order[i]].value)

    best = {"value": 0, "size": 0, "ids": ()}  # 空集恒可行
    stats = {"nodes": 0, "budget_exhausted": False}

    def record(value: int, size: int, ids: tuple[str, ...]) -> None:
        if (value > best["value"]
                or (value == best["value"] and size < best["size"])
                or (value == best["value"] and size == best["size"] and ids < best["ids"])):
            best["value"], best["size"], best["ids"] = value, size, ids

    def recurse(pos: int, selected: frozenset[str], excluded: frozenset[str],
                cur_value: int, cur_size: int, groups: frozenset[str]) -> None:
        stats["nodes"] += 1
        if max_nodes is not None and stats["nodes"] > max_nodes:
            raise _BudgetExhausted
        # 当前 selected 恒为可行解（闭包/互斥/容量在构造时保证），逐节点记录候选。
        record(cur_value, cur_size, tuple(sorted(selected)))
        while pos < n and (order[pos] in selected or order[pos] in excluded):
            pos += 1
        if pos == n:
            return
        if cur_value + suffix_pos[pos] < best["value"]:
            return  # 上界严格低于最佳已知值，剪枝（平局情形仍保留以处理平局裁决）
        iid = order[pos]
        # 分支一：纳入 iid（连带其依赖闭包）
        inc = closures[iid]
        if not (inc & excluded):
            new_items = inc - selected
            new_size = cur_size + sum(by_id[x].size for x in new_items)
            if new_size <= capacity:
                new_groups: dict[str, int] = {}
                ok = True
                for x in new_items:
                    g = by_id[x].mutex_group
                    if g is None:
                        continue
                    if g in groups or g in new_groups:
                        ok = False
                        break
                    new_groups[g] = 1
                if ok:
                    recurse(pos + 1, selected | inc, excluded,
                            cur_value + sum(by_id[x].value for x in new_items),
                            new_size, groups | frozenset(new_groups))
        # 分支二：排除 iid
        recurse(pos + 1, selected, excluded | {iid}, cur_value, cur_size, groups)

    try:
        recurse(0, frozenset(), frozenset(), 0, 0, frozenset())
    except _BudgetExhausted:
        stats["budget_exhausted"] = True

    return {
        "ids": list(best["ids"]),
        "total_value": best["value"],
        "total_size": best["size"],
        "proven_optimal": not stats["budget_exhausted"],
        "nodes_explored": stats["nodes"],
        "budget_exhausted": stats["budget_exhausted"],
    }


# ---------------------------------------------------------------------------
# 独立可行性复核（不复用求解器内部状态，从原始输入重新校验）
# ---------------------------------------------------------------------------

def verify_selection(items: list[Item], capacity: int, selected_ids: list[str]) -> dict:
    by_id = {it.id: it for it in items}
    selected = set(selected_ids)
    problems: list[str] = []

    if len(selected) != len(selected_ids):
        problems.append("选择清单含重复 id")
    unknown = sorted(selected - set(by_id))
    if unknown:
        problems.append(f"选择清单含未知 id: {unknown}")

    missing_deps: dict[str, list[str]] = {}
    for iid in sorted(selected & set(by_id)):
        miss = sorted(set(by_id[iid].dependencies) - selected)
        if miss:
            missing_deps[iid] = miss
    if missing_deps:
        problems.append(f"依赖闭包不完整: {missing_deps}")

    group_members: dict[str, list[str]] = {}
    for iid in sorted(selected & set(by_id)):
        g = by_id[iid].mutex_group
        if g is not None:
            group_members.setdefault(g, []).append(iid)
    conflicts = {g: m for g, m in group_members.items() if len(m) > 1}
    if conflicts:
        problems.append(f"互斥组冲突: {conflicts}")

    known = selected & set(by_id)
    total_size = sum(by_id[i].size for i in known)
    total_value = sum(by_id[i].value for i in known)
    if total_size > capacity:
        problems.append(f"超容量: 总大小 {total_size} > 容量 {capacity}")

    return {
        "feasible": not problems,
        "problems": problems,
        "checks": {
            "all_ids_known": not unknown,
            "dependency_closure_complete": not missing_deps,
            "mutex_groups_respected": not conflicts,
            "within_capacity": total_size <= capacity,
        },
        "recomputed_total_size": total_size,
        "recomputed_total_value": total_value,
    }


# ---------------------------------------------------------------------------
# 报告
# ---------------------------------------------------------------------------

def build_report(items: list[Item], capacity: int, max_nodes: int | None = None) -> dict:
    by_id = {it.id: it for it in items}
    result = solve(items, capacity, max_nodes)
    selected = set(result["ids"])
    topo, acyclic = topological_order(selected, by_id)
    recheck = verify_selection(items, capacity, result["ids"])
    return {
        "status": "proven_optimal" if result["proven_optimal"] else "best_known_not_proven_optimal",
        "proven_optimal": result["proven_optimal"],
        "capacity": capacity,
        "selection": {
            "ids": result["ids"],
            "total_value": result["total_value"],
            "total_size": result["total_size"],
        },
        "dependency_topology": {
            "edges": {iid: sorted(d for d in by_id[iid].dependencies if d in selected)
                      for iid in sorted(selected)},
            "topological_order": topo,
            "acyclic": acyclic,
        },
        "feasibility_recheck": recheck,
        "search_stats": {
            "nodes_explored": result["nodes_explored"],
            "node_budget": max_nodes,
            "budget_exhausted": result["budget_exhausted"],
        },
    }


# ---------------------------------------------------------------------------
# 命令行
# ---------------------------------------------------------------------------

def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="evidence_packer",
        description="容量约束证据打包：在容量、依赖闭包与互斥组约束下求最高价值交付包。",
    )
    parser.add_argument("input", help="JSON 输入文件路径，或 '-' 表示标准输入")
    parser.add_argument("--max-nodes", type=int, default=None,
                        help="分支限界节点预算；耗尽则结果仅为最佳已知而非已证最优")
    parser.add_argument("--pretty", action="store_true", help="美化输出 JSON")
    args = parser.parse_args(argv)

    try:
        text = sys.stdin.read() if args.input == "-" else open(args.input, encoding="utf-8").read()
        payload = json.loads(text)
        items, capacity, file_budget = parse_instance(payload)
    except (OSError, json.JSONDecodeError, InputError) as exc:
        print(f"输入错误: {exc}", file=sys.stderr)
        return 2

    max_nodes = args.max_nodes if args.max_nodes is not None else file_budget
    report = build_report(items, capacity, max_nodes)
    json.dump(report, sys.stdout, ensure_ascii=False,
              indent=2 if args.pretty else None, sort_keys=False)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
