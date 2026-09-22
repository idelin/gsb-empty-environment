"""Runnable demos: library queries, conflict explanations, sharing metrics.

Usage:
    python -m cdep.demo query    cdep/examples/dual_route.json
    python -m cdep.demo conflicts cdep/examples/conflict_demo.json
    python -m cdep.demo scale
"""

from __future__ import annotations

import sys

from .model import GraphSpec, Edge
from .analyzer import Analyzer, PUBLIC
from .bdd import TRUE


def _fmt_config(config):
    if not config:
        return "(no features needed)"
    return ", ".join(f"{k}={'on' if v else 'off'}" for k, v in sorted(config.items()))


def _fmt_path(path):
    if not path:
        return "<root itself>"
    out = [path[0].src]
    for s in path:
        out.append(f"--{s.kind}[{s.when}]-->{s.dst}")
    return " ".join(out)


def cmd_query(path: str) -> None:
    spec = GraphSpec.from_file(path)
    a = Analyzer(spec)
    print(f"root {a.root} | features: {a.bdd.var_order}")
    print(f"stats: {a.stats()}\n")
    for name in a.exposed_names():
        cond = a.condition_of(name)
        print(f"symbol {name!r} reachable when: {a.condition_text(cond)}")
        for src, scond in a.sources_of(name).items():
            w = a.witness_for(name, src)
            print(f"  source {src.pkg_id}@{src.version}::{src.symbol}"
                  f"  when: {a.condition_text(scond)}")
            print(f"    one config : {_fmt_config(w.config)}")
            print(f"    supply path: {_fmt_path(w.path)}")
        print()
    print("build closure (never runtime-exposed):")
    for node, cond in sorted(a.build_packages().items()):
        print(f"  {node}: {a.condition_text(cond)}")


def cmd_conflicts(path: str) -> None:
    spec = GraphSpec.from_file(path)
    a = Analyzer(spec)
    conflicts = a.conflicts()
    if not conflicts:
        print("No conflicts.")
    for c in conflicts:
        print(f"CONFLICT on symbol {c.name!r}")
        s1, s2 = c.sources
        print(f"  common feasible config: {_fmt_config(c.config)}")
        for w in (c.witnesses[0], c.witnesses[1]):
            print(f"  supplier {w.source.pkg_id}@{w.source.version}::"
                  f"{w.source.symbol}")
            print(f"    own cond: {a.condition_text(w.condition)}")
            print(f"    config  : {_fmt_config(w.config)}")
            print(f"    path    : {_fmt_path(w.path)}")
        print()


def cmd_scale() -> None:
    print("shared BDD representation: n independent feature-gated routes")
    print(f"{'n':>4} {'configs':>12} {'bdd_nodes':>10}")
    rows = []
    for n in (4, 8, 16, 32, 64):
        features = [f"f{i}" for i in range(2 * n)]
        packages = [{"id": "root@1", "provides": [],
                     "dependencies": {"public": []}}]
        for i in range(n):
            packages.append({"id": f"d{i}@1", "provides": ["sym"]})
            packages[0]["dependencies"]["public"].append(
                {"to": f"d{i}@1", "when": f"f{2*i} | f{2*i+1}"})
        spec = GraphSpec({"root": "root@1", "features": features,
                          "packages": packages})
        a = Analyzer(spec)
        configs = 2 ** (2 * n)
        rows.append((n, configs, a.bdd.size()))
        print(f"{n:>4} {configs:>12} {a.bdd.size():>10}")
    print("\nincremental: delete the cycle-entry edge in a 50-node cycle")
    n = 50
    packages = [{"id": f"p{i}@1", "provides": [f"s{i}"],
                 "dependencies": {"public": [
                     {"to": f"p{(i + 1) % n}@1", "when": "1"}]}}
                for i in range(n)]
    spec = GraphSpec({"root": "p0@1", "features": [], "packages": packages})
    a = Analyzer(spec)
    # extra back edge p25 -> p10 creates a second cycle entry region
    eid = a.add_edge(Edge(PUBLIC, "p40@1", "p10@1", TRUE, "1")).edge_id
    rep = a.remove_edge(eid)
    print(f"  touched graph nodes: {len(rep.touched_nodes)} "
          f"(of {n}); BDD node delta: {rep.bdd_nodes_delta}")
    print("  deleting cycle entry p0->p1:")
    entry = a._find_edge(PUBLIC, "p0@1", "p1@1")
    rep2 = a.remove_edge(entry)
    from .bdd import FALSE
    remaining = [k for k, v in a.rt_reach.items()
                 if v != FALSE and k != "p0@1"]
    print(f"  touched nodes: {len(rep2.touched_nodes)}; "
          f"nodes still reachable besides root: {len(remaining)}")
    assert not remaining, "cycle-supported reachability must be fully removed"
    print("  -> no stale reachability retained in the cycle (DRed), "
          "unlike plain reference counting")


COMMANDS = {"query": cmd_query, "conflicts": cmd_conflicts,
            "scale": lambda _: cmd_scale()}


def main(argv=None) -> int:
    argv = argv or sys.argv[1:]
    if not argv or argv[0] not in COMMANDS:
        print(__doc__)
        return 2
    if argv[0] == "scale":
        cmd_scale()
    else:
        if len(argv) < 2:
            print(__doc__)
            return 2
        COMMANDS[argv[0]](argv[1])
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
