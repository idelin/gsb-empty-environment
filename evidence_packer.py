#!/usr/bin/env python3
"""容量约束证据打包（capacity-constrained evidence packing）。

每个证据文件有大小、价值、依赖文件集合与互斥版本组。
在总大小不超过容量、依赖闭包完整、每组至多选一个的约束下，
选择总价值最高的交付包。平局时取更小总大小，再取 ID 字典序更小者。

重复依赖只计一次大小：交付包是文件集合，共享依赖自然只计入一次。

搜索支持节点预算：预算内穷竭搜索空间 => 已证最优（proven optimal）；
预算耗尽提前停止 => 仅返回最佳已知（best known），并明确标注未证最优。
"""
from __future__ import annotations

import argparse
import json
import sys
from dataclasses import dataclass
from typing import Optional


# ---------------------------------------------------------------- 数据模型

@dataclass(frozen=True)
class EvidenceFile:
    fid: str
    size: int
    value: int
    deps: tuple = ()
    group: Optional[str] = None


@dataclass(frozen=True)
class Instance:
    capacity: int
    files: dict  # fid -> EvidenceFile


def load_instance(obj) -> Instance:
    """从 dict（JSON 反序列化结果）构造并校验实例。"""
    if not isinstance(obj, dict):
        raise ValueError("顶层必须是 JSON 对象")
    capacity = obj.get("capacity")
    if not isinstance(capacity, int) or isinstance(capacity, bool) or capacity < 0:
        raise ValueError("capacity 必须是非负整数")
    raw_files = obj.get("files")
    if not isinstance(raw_files, list):
        raise ValueError("files 必须是数组")

    files = {}
    for entry in raw_files:
        if not isinstance(entry, dict):
            raise ValueError("files 数组元素必须是对象")
        fid = entry.get("id")
        if not isinstance(fid, str) or not fid:
            raise ValueError("每个文件需要非空字符串 id")
        if fid in files:
            raise ValueError(f"文件 id 重复: {fid!r}")
        size = entry.get("size")
        value = entry.get("value")
        for name, num in (("size", size), ("value", value)):
            if not isinstance(num, int) or isinstance(num, bool):
                raise ValueError(f"文件 {fid!r} 的 {name} 必须是整数")
        if size < 0:
            raise ValueError(f"文件 {fid!r} 的 size 不能为负")
        deps = entry.get("deps", [])
        if not isinstance(deps, list) or not all(isinstance(d, str) for d in deps):
            raise ValueError(f"文件 {fid!r} 的 deps 必须是字符串数组")
        group = entry.get("group")
        if group is not None and not isinstance(group, str):
            raise ValueError(f"文件 {fid!r} 的 group 必须是字符串或 null")
        files[fid] = EvidenceFile(fid=fid, size=size, value=value,
                                  deps=tuple(dict.fromkeys(deps)), group=group)

    for f in files.values():
        for dep in f.deps:
            if dep not in files:
                raise ValueError(f"文件 {f.fid!r} 依赖未知文件 {dep!r}")
    return Instance(capacity=capacity, files=files)


# ---------------------------------------------------------------- 求解器

class _BudgetExhausted(Exception):
    pass


@dataclass(frozen=True)
class Solution:
    files: tuple          # 排序后的文件 id 元组
    total_value: int
    total_size: int
    proven_optimal: bool  # True=已证最优；False=预算耗尽，仅为最佳已知
    nodes_explored: int
    node_budget: Optional[int]


def _better(cand, best) -> bool:
    """比较 (value, size, ids)：价值高者优先，平局更小总大小，再 ID 字典序。"""
    if cand[0] != best[0]:
        return cand[0] > best[0]
    if cand[1] != best[1]:
        return cand[1] < best[1]
    return cand[2] < best[2]


class Solver:
    """分支定界精确求解。include 分支会并入整个依赖闭包，保证闭包完整性。"""

    def __init__(self, instance: Instance, node_budget: Optional[int] = None):
        if node_budget is not None and node_budget < 1:
            raise ValueError("node_budget 必须为正整数或 None")
        self.instance = instance
        self.files = instance.files
        self.node_budget = node_budget
        self.nodes = 0
        self.closures = {fid: self._closure(fid) for fid in self.files}
        # 搜索顺序：价值密度降序（零大小正价值优先），id 兜底保证确定性。
        self.order = sorted(self.files, key=self._sort_key)
        # suffix_pos[i] = order[i:] 中所有正价值之和，用于上界剪枝。
        self.suffix_pos = [0] * (len(self.order) + 1)
        for i in range(len(self.order) - 1, -1, -1):
            v = self.files[self.order[i]].value
            self.suffix_pos[i] = self.suffix_pos[i + 1] + max(v, 0)
        # best: (value, size, ids_tuple)，空集恒可行作为初始解。
        self.best = (0, 0, ())

    def _sort_key(self, fid):
        f = self.files[fid]
        if f.size == 0:
            density = float("inf") if f.value > 0 else 0.0
        else:
            density = f.value / f.size
        return (-density, fid)

    def _closure(self, fid) -> frozenset:
        seen = set()
        stack = [fid]
        while stack:
            cur = stack.pop()
            if cur in seen:
                continue
            seen.add(cur)
            stack.extend(self.files[cur].deps)
        return frozenset(seen)

    def solve(self) -> Solution:
        exhausted = True
        try:
            self._dfs(0, frozenset(), frozenset(), frozenset(), 0, 0)
        except _BudgetExhausted:
            exhausted = False
        value, size, ids = self.best
        return Solution(files=ids, total_value=value, total_size=size,
                        proven_optimal=exhausted, nodes_explored=self.nodes,
                        node_budget=self.node_budget)

    def _dfs(self, i, included, excluded, used_groups, size, value):
        if self.node_budget is not None and self.nodes >= self.node_budget:
            raise _BudgetExhausted()
        self.nodes += 1
        # 上界剪枝：剩余正价值全拿也严格小于最优值时才剪，
        # 等于时不剪，以保证大小/字典序平局裁决的正确性。
        if value + self.suffix_pos[i] < self.best[0]:
            return
        if i == len(self.order):
            cand = (value, size, tuple(sorted(included)))
            if _better(cand, self.best):
                self.best = cand
            return
        fid = self.order[i]
        # 分支一：纳入 fid（连带其依赖闭包中尚未纳入的部分）。
        new_items = self.closures[fid] - included
        if not (new_items & excluded):
            new_size = size + sum(self.files[x].size for x in new_items)
            if new_size <= self.instance.capacity:
                new_groups = used_groups
                ok = True
                for x in sorted(new_items):
                    g = self.files[x].group
                    if g is not None:
                        if g in new_groups:
                            ok = False
                            break
                        new_groups = new_groups | {g}
                if ok:
                    new_value = value + sum(self.files[x].value for x in new_items)
                    self._dfs(i + 1, included | new_items, excluded,
                              new_groups, new_size, new_value)
        # 分支二：排除 fid（其依赖仍可被其他文件独立纳入）。
        self._dfs(i + 1, included, excluded | {fid}, used_groups, size, value)


# ---------------------------------------------------------------- 独立复核

def verify_selection(files: dict, capacity: int, selected) -> list:
    """独立可行性复核：不复用求解器任何中间状态，从零重算全部约束。

    返回问题描述列表，空列表表示可行。
    """
    problems = []
    selected = list(selected)
    if len(selected) != len(set(selected)):
        problems.append("交付清单包含重复文件 id")
    sel = set(selected)
    for fid in sorted(sel):
        if fid not in files:
            problems.append(f"清单包含未知文件 {fid!r}")
    known = sel & files.keys()
    for fid in sorted(known):
        for dep in files[fid].deps:
            if dep not in sel:
                problems.append(f"依赖闭包不完整: {fid!r} 需要 {dep!r}")
    groups = {}
    for fid in sorted(known):
        g = files[fid].group
        if g is not None:
            if g in groups:
                problems.append(
                    f"互斥组冲突: {groups[g]!r} 与 {fid!r} 同属组 {g!r}")
            else:
                groups[g] = fid
    total = sum(files[fid].size for fid in known)
    if total > capacity:
        problems.append(f"超出容量: 总大小 {total} > 容量 {capacity}")
    return problems


# ---------------------------------------------------------------- 报告

def build_report(instance: Instance, solution: Solution) -> dict:
    files = instance.files
    selected = list(solution.files)
    topology = {fid: sorted(files[fid].deps) for fid in selected}
    problems = verify_selection(files, instance.capacity, selected)
    return {
        "status": "optimal" if solution.proven_optimal else "best_known",
        "proven_optimal": solution.proven_optimal,
        "total_value": solution.total_value,
        "total_size": solution.total_size,
        "capacity": instance.capacity,
        "files": selected,
        "dependency_topology": topology,
        "verification": {
            "feasible": not problems,
            "problems": problems,
        },
        "search": {
            "nodes_explored": solution.nodes_explored,
            "node_budget": solution.node_budget,
            "budget_exhausted": not solution.proven_optimal,
        },
    }


# ---------------------------------------------------------------- CLI

def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        description="容量约束证据打包：选出不超容量、依赖闭包完整的最高价值交付包。")
    parser.add_argument("input", help="输入 JSON 文件路径（'-' 表示标准输入）")
    parser.add_argument("--budget", type=int, default=None, metavar="N",
                        help="搜索节点预算；耗尽则输出最佳已知并标注未证最优")
    parser.add_argument("--compact", action="store_true",
                        help="紧凑输出（默认缩进 2 格）")
    args = parser.parse_args(argv)

    try:
        raw = sys.stdin.read() if args.input == "-" else open(
            args.input, encoding="utf-8").read()
        instance = load_instance(json.loads(raw))
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        print(f"输入错误: {exc}", file=sys.stderr)
        return 2

    try:
        solution = Solver(instance, node_budget=args.budget).solve()
    except ValueError as exc:
        print(f"参数错误: {exc}", file=sys.stderr)
        return 2

    report = build_report(instance, solution)
    indent = None if args.compact else 2
    print(json.dumps(report, ensure_ascii=False, indent=indent))

    if not report["verification"]["feasible"]:
        print("警告: 独立可行性复核未通过，结果未验证。", file=sys.stderr)
        return 1
    if not solution.proven_optimal:
        print("注意: 节点预算耗尽，结果为最佳已知，未证最优。", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
