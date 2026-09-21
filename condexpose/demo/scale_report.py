"""报告：特性数增长时 ROBDD 节点数（共享）与增量删除触及的图节点。

输出为 Markdown 表格。第一组图：N 个互斥分支包暴露同名符号
（每分支一个特性），暴露条件共享同一条链式 ROBDD；
第二组：菱形 + 条件，在规模增长时删除一条边，报告被触及事实/包数。
"""

from __future__ import annotations

from condexpose import ExposureAnalyzer, PackageGraph
from condexpose.bdd import FALSE


def reachable_node_count(bdd, roots):
    seen = {FALSE, 1}
    stack = list(roots)
    while stack:
        node = stack.pop()
        if node in seen:
            continue
        seen.add(node)
        stack.extend((bdd.low_of[node], bdd.high_of[node]))
    return len(seen)


def exclusive_branches(n):
    features = [f"f{i}" for i in range(n)]
    packages = [{"id": "root", "version": "1"}]
    edges = []
    for index, feature in enumerate(features):
        packages.append({"id": f"p{index}", "version": "1",
                         "symbols": ["shared"]})
        edges.append({"from": "root", "to": f"p{index}",
                      "kind": "public", "when": feature, "key": index})
    return {"features": features, "packages": packages, "edges": edges}


def diamond_with_features(n):
    features = [f"f{i}" for i in range(n)]
    packages = [
        {"id": "root", "version": "1"},
        {"id": "mid", "version": "1"},
        {"id": "leaf", "version": "1", "symbols": ["v"]},
    ]
    condition = ["and"] + features
    edges = [
        {"from": "root", "to": "mid", "kind": "public", "key": 0},
        {"from": "mid", "to": "leaf", "kind": "public",
         "when": condition, "key": 1},
        {"from": "root", "to": "leaf", "kind": "public",
         "when": condition, "key": 2},
    ]
    return {"features": features, "packages": packages, "edges": edges}


def cycle_entry_graph(n):
    features = [f"f{i}" for i in range(n)]
    cond = ["or"] + features
    packages = [
        {"id": "root", "version": "1"},
        {"id": "a", "version": "1", "symbols": ["sa"]},
        {"id": "b", "version": "1", "symbols": ["sb"]},
    ]
    edges = [
        {"from": "root", "to": "a", "kind": "public", "when": cond, "key": 0},
        {"from": "a", "to": "b", "kind": "public", "key": 1},
        {"from": "b", "to": "a", "kind": "public", "key": 2},
    ]
    return {"features": features, "packages": packages, "edges": edges}


def main():
    print("## ROBDD 共享规模：N 个特性分支暴露同名符号\n")
    print("| 特性数 N | 配置数 2^N | 暴露条件共享 ROBDD 可达节点 | BDD 唯一表节点 |")
    print("|---:|---:|---:|---:|")
    for n in (2, 4, 8, 16, 32, 64):
        data = exclusive_branches(n)
        analyzer = ExposureAnalyzer(PackageGraph(data))
        records = analyzer.exposure_condition("shared")
        live = reachable_node_count(
            analyzer.bdd, [record.condition for record in records])
        print(f"| {n} | {2 ** n} | {live} | {analyzer.bdd.node_count()} |")

    print("\n## 增量删除触及的图节点：菱形删一条边（条件含 N 特性）\n")
    print("| 特性数 N | 过度删除事实 | 值变化事实 | 触及包 | 删除后另一路径仍可见 |")
    print("|---:|---:|---:|---:|:---:|")
    for n in (1, 2, 4, 8, 16, 32):
        data = diamond_with_features(n)
        analyzer = ExposureAnalyzer(PackageGraph(data))
        report = analyzer.delete_edge(1)
        still_visible = bool(analyzer.exposure_condition("v"))
        print(f"| {n} | {report.facts_overdeleted} | "
              f"{report.facts_touched} | {report.packages_touched} | "
              f"{'是' if still_visible else '否'} |")

    print("\n## 删除环入口：循环旧可达性清除（入口边条件含 N 特性）\n")
    print("| 特性数 N | 过度删除事实 | 值变化事实 | 触及包 | 删除后运行包（应仅 root） |")
    print("|---:|---:|---:|---:|---|")
    for n in (1, 2, 4, 8, 16):
        data = cycle_entry_graph(n)
        analyzer = ExposureAnalyzer(PackageGraph(data))
        report = analyzer.delete_edge(0)
        remaining = ",".join(analyzer.runtime_packages())
        print(f"| {n} | {report.facts_overdeleted} | "
              f"{report.facts_touched} | {report.packages_touched} | "
              f"{remaining} |")


if __name__ == "__main__":
    main()
