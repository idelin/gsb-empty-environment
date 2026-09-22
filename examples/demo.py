#!/usr/bin/env python3
"""Reproducible end-to-end demonstration.

Run with::

    PYTHONPATH=src python3.11 examples/demo.py

Everything is deterministic: there is no randomness, no network and no
third-party dependency.  The example walks through

1. one-time evaluation of a shared-subtree DAG,
2. occurrence-sensitive evidence for one subtree under two parents,
3. strict arity separation (same label, different arity),
4. a deep leaf edit that recomputes ancestors only,
5. old snapshots remaining queryable,
6. rule addition and rule deletion with support-based retraction,
7. least-fixpoint semantics for rule cycles (ungrounded cycles do nothing),
8. resource limits and cooperative cancellation.
"""
from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from statree import (  # noqa: E402
    Automaton,
    CancellationError,
    CountdownCancel,
    LimitExceeded,
    Limits,
)

ALPHABET = ("a", "b", "k", "x", "y")

# Node 0 (a red 'a' leaf) is shared by BOTH children of node 2.
# Node 2 is in turn shared by the two roots 3 and 4.
RAW_NODES = [
    (0, "a", [], {"color": "red"}),
    (1, "a", [], {"color": "blue"}),
    (2, "b", [0, 0]),
    (3, "b", [2, 1]),
    (4, "b", [2, 0]),
    (5, "k", [0]),
    (6, "x", []),
    (7, "y", [6]),
]

INITIAL_RULES = [
    {"name": "leaf", "state": "qLeaf", "label": "a", "child_states": []},
    {
        "name": "red-a",
        "state": "qA",
        "label": "a",
        "child_states": [],
        "condition": ("color", "==", "red"),
    },
    {
        "name": "red-pair",
        "state": "qB",
        "label": "b",
        "child_states": [{"qA"}, {"qA"}],
    },
    {
        "name": "root-rule",
        "state": "qRoot",
        "label": "b",
        "child_states": [{"qB"}, {"qLeaf"}],
    },
]

ACCEPTING = [{"states": {"qRoot", "qB", "qK1", "qK2", "qPair", "qF"}}]


def section(title: str) -> None:
    print()
    print("=" * 72)
    print(title)
    print("=" * 72)


def show_states(auto: Automaton) -> None:
    print("state table:")
    for nid in sorted(auto.dag.ids()):
        print(f"  node {nid} ({auto.dag.node(nid).label}/{auto.dag.node(nid).arity}): "
              f"{sorted(auto.states(nid))}")


def main() -> int:
    section("1. Shared-subtree DAG evaluated bottom-up, one pass")
    auto = Automaton(
        ALPHABET,
        RAW_NODES,
        INITIAL_RULES,
        roots={"left": 3, "right": 4},
        signatures=ACCEPTING,
    )
    show_states(auto)
    print(f"rule-match attempts for the whole DAG: {auto.stats.firings}")
    print("accepting:", {k: sorted(v) for k, v in auto.accepting().items()})

    section("2. Same subtree id=0, two parent occurrences -> distinct evidence")
    witnesses = auto.explain(2, "qB")
    print(f"qB proofs at node 2: {len(witnesses)}")
    for w in witnesses:
        paths = [c.path_str for c in w.children]
        print(f"  via {w.rule}: child occurrence paths {paths}, "
              f"child node ids {[c.node_id for c in w.children]}")
    assert [c.node_id for c in witnesses[0].children] == [0, 0]
    assert [c.path_str for c in witnesses[0].children] == ["$[0]", "$[1]"]

    section("3. Same label 'k' but different arity must not mix")
    auto.add_rule(
        {"name": "k-unary", "state": "qK1", "label": "k",
         "child_states": [{"qLeaf"}]}
    )
    auto.add_rule(
        {"name": "k-binary", "state": "qK2", "label": "k",
         "child_states": [{"qLeaf"}, {"qLeaf"}]}
    )
    print("node 5 is k/1:", sorted(auto.states(5)))
    print("qK2 (binary) never applies to k/1:", "qK2" not in auto.states(5))
    assert auto.states(5) == frozenset({"qK1"})

    section("4. Deep leaf edit: only the leaf and ancestors recompute")
    before = auto.stats.firings
    report = auto.edit_leaf("left", (1,), label="a", attrs={"color": "red"})
    print("edit report:", report)
    print(f"rule-match attempts during edit: {auto.stats.firings} "
          f"(rebuilt spine ids {list(report.rebuilt)})")
    untouched = 2
    print(f"shared node {untouched} state cache retained: {sorted(auto.states(untouched))}")

    section("5. Old snapshot still answers queries after the edit")
    old = auto.snapshot(1)
    print("snapshot v1 root 'left' accepting:", sorted(old.accepting_states("left")))
    snap_w = old.explain(old.roots["left"], "qRoot")
    print("snapshot v1 still yields qRoot witnesses:", len(snap_w))
    print("live accepting after edit:",
          {k: sorted(v) for k, v in auto.accepting().items()})
    snap_after_edit = auto.commit()

    section("6. Rule addition then deletion with support-based retraction")
    auto.add_rule(
        {"name": "blue-a", "state": "qA", "label": "a", "child_states": [],
         "condition": ("color", "==", "blue")}
    )
    print("after adding blue-a, node 1:", sorted(auto.states(1)))
    print("root 'left' accepting now:", sorted(auto.accepting()["left"]))
    auto.delete_rule("blue-a")
    print("after deleting blue-a, node 1:", sorted(auto.states(1)))
    print("root 'left' accepting restored:", sorted(auto.accepting()["left"]))

    section("6b. Alternate justification keeps a state; second delete drops it")
    auto.add_rule(
        {"name": "red-pair-copy", "state": "qB", "label": "b",
         "child_states": [{"qA"}, {"qA"}]}
    )
    auto.delete_rule("red-pair")
    print("qB still supported by copy:", "qB" in auto.states(2))
    assert "qB" in auto.states(2)
    auto.delete_rule("red-pair-copy")
    print("qB gone after the copy is also deleted:", "qB" not in auto.states(2))
    print("qRoot retracted transitively at roots:",
          all("qRoot" not in auto.states(r) for r in (3, 4, report.rebuilt[0])))

    section("7. Rule cycles: least fixpoint never grounds unsupported states")
    cyclic_dag = [
        (10, "x", []),
        (11, "y", [10]),
    ]
    cyclic_rules = [
        {"name": "x-to-y", "state": "qY", "label": "y",
         "child_states": [{"qX"}]},
        {"name": "y-to-x", "state": "qX", "label": "x", "child_states": []},
    ]
    cyc = Automaton(
        ("x", "y"),
        cyclic_dag,
        cyclic_rules[:1],
        roots={"c": 11},
        signatures=[{"states": {"qY"}}],
    )
    print("with only qY:- qX, nothing is derived:",
          dict((i, set(cyc.states(i))) for i in cyc.dag.ids()))
    cyc.add_rule(cyclic_rules[1])
    print("once x/0 grounds qX, qY appears:",
          {10: set(cyc.states(10)), 11: set(cyc.states(11))})
    cyc.delete_rule("y-to-x")
    print("deleting the ground rule empties the cycle:",
          {10: set(cyc.states(10)), 11: set(cyc.states(11))})

    section("8. Resource limits and cancellation")
    limited = Automaton(
        ALPHABET,
        RAW_NODES,
        INITIAL_RULES,
        roots={"left": 3},
        signatures=ACCEPTING,
    )
    limited.limits = Limits(max_firings=2)
    try:
        limited.evaluate_all()
    except LimitExceeded as exc:
        print("max_firings raised:", exc)
    limited.limits = Limits()
    token = CountdownCancel(3)
    bounded = Automaton(
        ALPHABET,
        RAW_NODES,
        INITIAL_RULES,
        roots={"left": 3},
        signatures=ACCEPTING,
    )
    try:
        bounded.evaluate_all(token=token)
    except CancellationError as exc:
        print("countdown token raised:", exc)
    print("a fresh token lets evaluation finish:",
          bool(bounded.evaluate_all()) )

    section("Summary")
    print("snapshots kept:", sorted(auto.snapshot(v).version for v in (1, snap_after_edit.version)))
    print("DEMO OK")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
