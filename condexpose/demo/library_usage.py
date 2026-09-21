"""可查询库的最小使用示例。"""

from condexpose import ExposureAnalyzer, PackageGraph

DATA = {
    "features": ["opt"],
    "packages": [
        {"id": "root", "version": "1.0"},
        {"id": "fast", "version": "1.0", "symbols": ["run"]},
        {"id": "slow", "version": "0.9", "symbols": ["run"]},
    ],
    "edges": [
        {"from": "root", "to": "fast", "kind": "public", "when": "opt"},
        {"from": "root", "to": "slow", "kind": "public", "when": ["not", "opt"]},
    ],
}


def main():
    graph = PackageGraph(DATA)
    analyzer = ExposureAnalyzer(graph)

    for record in analyzer.exposures():
        print(record.name, record.source.package,
              "condition-node:", record.condition,
              "model:", graph.bdd.one_model(record.condition))

    print("conflicts:", analyzer.conflicts())  # 互斥 -> []

    witness = analyzer.witness("run")
    print("witness config:", witness.assignment)
    print("witness hops:", witness.hops)

    # 增量：把 fast 改为恒开；此时在 opt=False 下 fast 与 slow 同时供应 -> 冲突
    report = analyzer.modify_edge(0, True)
    print("after modify, conflicts:",
          [(c.name, graph.bdd.one_model(c.condition))
           for c in analyzer.conflicts()])
    print("incremental facts touched:", report.facts_touched)


if __name__ == "__main__":
    main()
