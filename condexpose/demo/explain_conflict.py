"""冲突解释样例：共同可行配置 + 两条独立供应路径。

用法: python3 -m condexpose.demo.explain_conflict [graph.json]
默认读取同目录 demo_graph.json。
"""

from __future__ import annotations

import os
import sys

from condexpose import ExposureAnalyzer, PackageGraph


def describe_hop(analyzer, hop):
    if hop[0] == "own":
        _, package, symbol = hop
        return f"    起源: 包 {package} 自有导出符号 {symbol!r}"
    _, edge_key, at_package, visible = hop
    edge = analyzer.graph.edges[edge_key]
    kind = {"public": "public", "private": "private", "build": "build"}[
        edge.kind]
    return (f"    沿 {kind} 边 {edge.from_} <- {edge.to}"
            f" (边#{edge_key})，{at_package} 处可见名 {visible!r}")


def explain(path: str) -> int:
    graph = PackageGraph.from_file(path)
    analyzer = ExposureAnalyzer(graph)

    print(f"== 包图: {path}")
    print(f"特性: {graph.bdd.features}")
    print("\n-- 运行暴露符号 --")
    for record in analyzer.exposures():
        model = graph.bdd.one_model(record.condition)
        print(f"  {record.name!r} <- {record.source.package}"
              f"@{record.source.version}:{record.source.symbol}"
              f"  样例配置={model}")

    print("\n-- 构建闭包（不进入运行暴露）--")
    for record in analyzer.build_closure():
        print(f"  {record.name}  <- {record.source.package}")

    conflicts = analyzer.conflicts()
    if not conflicts:
        print("\n无冲突。")
        return 0

    print(f"\n-- 检测到 {len(conflicts)} 个同名冲突 --")
    for index, conflict in enumerate(conflicts, 1):
        assignment = graph.bdd.one_model(conflict.condition)
        print(f"\n[冲突 {index}] 符号 {conflict.name!r}")
        left_src, right_src = conflict.sources
        print(f"  来源 A: {left_src.package}@{left_src.version}:"
              f"{left_src.symbol}")
        print(f"  来源 B: {right_src.package}@{right_src.version}:"
              f"{right_src.symbol}")
        print(f"  共同可行配置: {assignment}")
        paths = analyzer.conflict_paths(conflict, assignment)
        if paths is None:
            print("  (无法取证)")
            continue
        for label, supply in zip(("A", "B"), paths):
            print(f"  供应路径 {label}:")
            for hop in supply.hops:
                print(describe_hop(analyzer, hop))
            print(f"    => root 暴露 {supply.name!r}")
    return 0


if __name__ == "__main__":
    target = sys.argv[1] if len(sys.argv) > 1 else os.path.join(
        os.path.dirname(__file__), "demo_graph.json")
    raise SystemExit(explain(target))
