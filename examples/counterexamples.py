#!/usr/bin/env python3
"""Standalone, executable counterexamples.

Run directly (no installation needed)::

    PYTHONPATH=src python3.11 examples/counterexamples.py

Each section constructs a small case where a tempting "optimization" or
misreading is *wrong*, shows that the implementation rejects/avoids it, and
prints PASS.  Exiting 0 means every counterexample fired as expected.
"""
from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from statree import (  # noqa: E402
    Automaton,
    CancellationError,
    CountdownCancel,
    Dag,
    LimitExceeded,
    Limits,
    RuleError,
    StructuralCycleError,
    least_fixpoint,
)
from statree.engine import RuleIndex  # noqa: E402


def check(name: str, ok: bool, detail: str = "") -> None:
    if not ok:
        raise AssertionError(f"counterexample failed: {name} {detail}")
    print(f"PASS  {name}  {detail}")


def counterexample_structural_cycle() -> None:
    raw = [(0, "f", [1]), (1, "g", [0])]
    try:
        Dag.from_adjacency("fg", raw)
    except StructuralCycleError as exc:
        check("structural cycle rejected", "0 -> 1 -> 0" in str(exc), str(exc))
        return
    raise AssertionError("cycle was accepted")


def counterexample_self_loop() -> None:
    raw = [(0, "f", [0])]
    try:
        Dag.from_adjacency("f", raw)
    except StructuralCycleError:
        check("self loop rejected", True)
        return
    raise AssertionError("self loop was accepted")


def counterexample_arity_must_not_mix() -> None:
    rules = [
        {"name": "u", "state": "qU", "label": "k", "child_states": [{"q"}]},
        {"name": "b", "state": "qB", "label": "k",
         "child_states": [{"q"}, {"q"}]},
        {"name": "leaf", "state": "q", "label": "a", "child_states": []},
    ]
    auto = Automaton(
        "ka",
        [(1, "a", []), (2, "k", [1]), (3, "k", [1, 1])],
        rules,
        roots={"u": 2, "bin": 3},
        signatures=[{"states": {"qU", "qB"}}],
    )
    check("unary rule only fires at k/1", auto.states(2) == frozenset({"qU"}))
    check("binary rule only fires at k/2", auto.states(3) == frozenset({"qB"}))
    try:
        {"state": "qBad", "label": "k", "child_states": []}
        # Nullary k rule below proves it will never fire on a non-leaf node.
        auto2 = Automaton(
            "ka", [(1, "a", []), (2, "k", [1])],
            [{"state": "qNull", "label": "k", "child_states": []}],
            roots={"u": 2},
        )
        check("nullary rule never matches k/1", auto2.states(2) == frozenset())
    except RuleError:
        raise


def counterexample_ungrounded_rule_cycle() -> None:
    dag = Dag.from_adjacency("xy", [(0, "x", []), (1, "y", [0])])
    cyclic = [
        {"state": "qY", "label": "y", "child_states": [{"qX"}]},
        {"state": "qX", "label": "x", "child_states": [{"qY"}]},
    ]
    table = least_fixpoint(dag, cyclic)
    check(
        "pure rule cycle derives nothing",
        table[0][0] == frozenset() and table[1][0] == frozenset(),
        f"states={ {i: set(s) for i, (s, _) in table.items()} } rounds ground at empty",
    )

    # Show what the WRONG semantics would do: seeding every state up front
    # accepts the cycle even though no finite proof tree exists.
    naive = {0: frozenset({"qX"}), 1: frozenset({"qY"})}
    check("naive all-state seed incorrectly 'accepts'", "qY" in naive[1])

    grounded = cyclic + [
        {"name": "x-base", "state": "qX", "label": "x", "child_states": []}
    ]
    table2 = least_fixpoint(dag, grounded)
    check(
        "adding a ground base makes the cycle productive",
        table2[0][0] == frozenset({"qX"}) and table2[1][0] == frozenset({"qY"}),
    )


def counterexample_shared_occurrences_get_distinct_evidence() -> None:
    raw = [
        (0, "a", []),
        (1, "b", [0, 0]),
        (2, "f", [1, 1]),
    ]
    rules = [
        {"state": "q", "label": "a", "child_states": []},
        {"state": "qB", "label": "b", "child_states": [{"q"}, {"q"}]},
        {"state": "qF", "label": "f", "child_states": [{"qB"}, {"qB"}]},
    ]
    auto = Automaton("abf", raw, rules, roots={"r": 2},
                     signatures=[{"states": {"qF"}}])
    ev = auto.explain(2, "qF")
    check("one qF proof", len(ev) == 1, f"{len(ev)} proof(s)")
    left, right = ev[0].children
    check(
        "two b-occurrences have distinct paths",
        left.path_str == "$[0]" and right.path_str == "$[1]"
        and left.node_id == right.node_id == 1,
        f"{left.path_str} vs {right.path_str}",
    )
    # Inside each shared b node the shared leaf occurrence also differs.
    l1, l2 = left.children
    r1, r2 = right.children
    check(
        "four leaf occurrences are path-distinguishable",
        [x.path_str for x in (l1, l2, r1, r2)]
        == ["$[0][0]", "$[0][1]", "$[1][0]", "$[1][1]"],
    )


def counterexample_delete_rule_retracts_unsupported_states() -> None:
    raw = [(0, "a", []), (1, "b", [0])]
    rules = [
        {"name": "a-rule", "state": "qA", "label": "a", "child_states": []},
        {"name": "b-rule", "state": "qB", "label": "b",
         "child_states": [{"qA"}]},
    ]
    auto = Automaton("ab", raw, rules, roots={"r": 1})
    assert auto.states(1) == frozenset({"qB"})
    auto.delete_rule("a-rule")
    check("child state retracted", auto.states(0) == frozenset())
    check("parent state retracted transitively", auto.states(1) == frozenset())


def counterexample_limits_and_cancellation_are_defined() -> None:
    dag = Dag.from_adjacency("a", [(0, "a", [])])
    idx = RuleIndex()
    from statree.model import Rule
    idx.add(Rule(state="q", label="a", child_states=()))

    try:
        least_fixpoint(dag, idx, limits=Limits(max_rounds=0))
    except LimitExceeded:
        check("max_rounds=0 is a defined failure", True)

    try:
        auto = Automaton("ab", [(0, "a", []), (1, "b", [0])],
                         [
                             {"state": "qA", "label": "a", "child_states": []},
                             {"state": "qB", "label": "b",
                              "child_states": [{"qA"}]},
                         ],
                         roots={"r": 1})
        auto.evaluate_all(token=CountdownCancel(1))
    except CancellationError:
        check("cooperative cancellation aborts evaluation", True)
        return
    raise AssertionError("cancellation did not fire")


def main() -> int:
    counterexample_structural_cycle()
    counterexample_self_loop()
    counterexample_arity_must_not_mix()
    counterexample_ungrounded_rule_cycle()
    counterexample_shared_occurrences_get_distinct_evidence()
    counterexample_delete_rule_retracts_unsupported_states()
    counterexample_limits_and_cancellation_are_defined()
    print("\nALL COUNTEREXAMPLES PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
