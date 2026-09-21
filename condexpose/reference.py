"""独立参照实现：对每个完整特性指派做一次朴素最小不动点遍历。

仅用于小图测试比对，刻意不共享 ROBDD 代码；直接对布尔指派求值。
"""

from __future__ import annotations

from itertools import product
from typing import Any

from .model import BUILD, PRIVATE, PUBLIC, ROOT_ID, PackageGraph, Source


def all_assignments(graph: PackageGraph) -> list[dict[str, bool]]:
    features = graph.bdd.features
    return [
        dict(zip(features, values))
        for values in product((False, True), repeat=len(features))
    ]


def _eval(node: int, graph: PackageGraph, assignment: dict[str, bool]) -> bool:
    return graph.bdd.evaluate(node, assignment)


def _apply_policy(pkg, incoming_name: str) -> str | None:
    if incoming_name in pkg.hide:
        return None
    for exposed, original in pkg.rename.items():
        if original == incoming_name:
            return exposed
    return incoming_name


def _apply_policy_all(pkg, incoming_name: str) -> list[str]:
    """一个原名可经 exports 扇出为多个别名；hide 优先。"""
    if incoming_name in pkg.hide:
        return []
    aliases = [exposed for exposed, original in pkg.rename.items()
               if original == incoming_name]
    return aliases or [incoming_name]


def analyze_assignment(graph: PackageGraph, assignment: dict[str, bool]):
    """返回 (runtime_exposures, build_closure_names, reachable_r, reachable_b)。

    runtime_exposures: name -> {source_tuple}
    """
    packages = graph.packages
    active = [
        edge for edge in graph.active_edges()
        if _eval(edge.condition, graph, assignment)
    ]

    # ---- 包可达性闭包 ----
    reach_r = {ROOT_ID}
    changed = True
    while changed:
        changed = False
        for edge in active:
            if edge.kind == PUBLIC and edge.from_ in reach_r and edge.to not in reach_r:
                reach_r.add(edge.to)
                changed = True

    direct_private = {
        edge.to for edge in active
        if edge.kind == PRIVATE and edge.from_ == ROOT_ID
    }

    reach_b: set[str] = set()
    if any(edge.kind == BUILD and edge.from_ == ROOT_ID for edge in active):
        reach_b.add(ROOT_ID)
    changed = True
    while changed:
        changed = False
        for edge in active:
            if edge.kind == BUILD and edge.from_ in reach_b and edge.to not in reach_b:
                reach_b.add(edge.to)
                changed = True

    # ---- 符号向上传播 ----
    def flow_fixpoint(reachable: set[str], scope_kind: str):
        # namespace[pkg][name] = set(source_tuple)
        namespace: dict[str, dict[str, set[tuple]]] = {
            pkg_id: {} for pkg_id in packages
        }
        for pkg_id in reachable:
            pkg = packages[pkg_id]
            for symbol in pkg.symbols:
                namespace[pkg_id].setdefault(symbol, set()).add(
                    Source(pkg_id, pkg.version, symbol).as_tuple())
        changed = True
        while changed:
            changed = False
            for edge in active:
                if scope_kind == "rt":
                    if edge.kind != PUBLIC:
                        continue
                    if edge.to not in reachable:
                        continue
                else:
                    if edge.kind != BUILD:
                        continue
                importer = packages[edge.from_]
                for name, sources in list(namespace[edge.to].items()):
                    for visible in _apply_policy_all(importer, name):
                        bucket = namespace[edge.from_].setdefault(visible, set())
                        before = len(bucket)
                        bucket.update(sources)
                        if len(bucket) != before:
                            changed = True
        return namespace

    rt_ns = flow_fixpoint(reach_r, "rt")
    build_ns = flow_fixpoint(reach_b, "build")

    # root 的自有符号也经过 root 的 exports 策略
    root_exposure: dict[str, set[tuple]] = {}
    root_pkg = packages[ROOT_ID]
    for name, sources in rt_ns[ROOT_ID].items():
        for visible in _apply_policy_all(root_pkg, name):
            root_exposure.setdefault(visible, set()).update(sources)

    # private 边：root 只直接看到该依赖“自身”的导出，传递被阻断
    for edge in active:
        if edge.kind != PRIVATE or edge.from_ != ROOT_ID:
            continue
        dep_pkg = packages[edge.to]
        for symbol in dep_pkg.symbols:
            for visible in _apply_policy_all(root_pkg, symbol):
                root_exposure.setdefault(visible, set()).add(
                    Source(edge.to, dep_pkg.version, symbol).as_tuple())

    # build 闭包内部名字（每个包自己的视角）
    build_names: dict[str, set[tuple]] = {}
    for pkg_id in reach_b:
        for name, sources in build_ns[pkg_id].items():
            for source in sources:
                build_names[f"{pkg_id}::{name}"] = build_names.get(
                    f"{pkg_id}::{name}", set()) | {source}

    return root_exposure, build_names, reach_r, reach_b


def reference_exposures(graph: PackageGraph):
    """返回 {assignment_tuple: {name: set(source_tuple)}}。"""
    result = {}
    for assignment in all_assignments(graph):
        exposure, _, _, _ = analyze_assignment(graph, assignment)
        key = tuple(assignment[name] for name in graph.bdd.features)
        result[key] = exposure
    return result
