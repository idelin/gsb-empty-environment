#!/usr/bin/env python3
"""Runnable end-to-end demonstration on synthetic data.

Run with:  PYTHONPATH=src python3.11 demo.py
Every fact, rule and derivation step carries a stable id and version, and the
evidence bundles produced here replay byte-identically through the independent
verifier.
"""

from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "src"))

from supportdag import (  # noqa: E402
    ANY,
    NOT,
    KnowledgeBase,
    Rule,
    brute_force_minimal_supports,
    min_cardinality_supports,
    verify_evidence,
)


def fmt(support) -> str:
    return "{" + ",".join(sorted(support.facts)) + "}"


def section(title: str) -> None:
    print()
    print("=" * 72)
    print(title)
    print("=" * 72)


def main() -> int:
    failures = []

    # ------------------------------------------------------------------ #
    section("1. Dominance: {A} dominates {A,B}, but never {C}")
    kb = KnowledgeBase()
    kb.commit(
        kb.add_fact("A", "a"),
        kb.add_fact("B", "b"),
        kb.add_fact("C", "c"),
        kb.add_rule(Rule("r_ab", "g", ("a", "b"), version=1)),
        kb.add_rule(Rule("r_a", "g", ("a",), version=1)),
        kb.add_rule(Rule("r_c", "h", ("c",), version=1)),
    )
    g = kb.query("g", budget=500)
    h = kb.query("h", budget=500)
    g_sets = {frozenset(s.facts) for s in g.supports}
    h_sets = {frozenset(s.facts) for s in h.supports}
    print("minimal supports of g:", sorted(map(set, g_sets)))
    print("minimal supports of h:", sorted(map(set, h_sets)))
    if g_sets != {frozenset({"A"})} or h_sets != {frozenset({"C"})}:
        failures.append("dominance")

    # ------------------------------------------------------------------ #
    section("2. Retraction makes a pruned alternative proof reappear")
    kb2 = KnowledgeBase()
    kb2.commit(
        kb2.add_fact("FB", "b"),
        kb2.add_rule(Rule("r_b", "g", ("b", NOT("a")), version=1)),
        kb2.add_rule(Rule("r_a", "g", ("a",), version=1)),
    )
    before = {frozenset(s.facts) for s in kb2.query("g", 500).supports}
    print("initial:              ", sorted(map(set, before)))
    kb2.commit(kb2.add_fact("FA", "a"))
    added = {frozenset(s.facts) for s in kb2.goal("g").supports}
    print("after adding A:       ", sorted(map(set, added)))
    report = kb2.commit(kb2.retract_fact("FA"))
    after = {frozenset(s.facts) for s in kb2.goal("g").supports}
    print("after retracting A:   ", sorted(map(set, after)),
          "revived:", report.revived_goals)
    if before != {frozenset({"FB"})} or added != {frozenset({"FA"})} or after != before:
        failures.append("reappearance")

    # ------------------------------------------------------------------ #
    section("3. Negative premise defeated by a new fact")
    kb3 = KnowledgeBase()
    kb3.commit(
        kb3.add_fact("FB", "b"),
        kb3.add_rule(Rule("r", "g", ("b", NOT("a")))),
    )
    print("before:", [fmt(s) for s in kb3.query("g", 100).supports])
    kb3.commit(kb3.add_fact("FA", "a"))
    neg_view = kb3.goal("g")
    print("after A is asserted:", [fmt(s) for s in neg_view.supports],
          f"(status={neg_view.status})")
    if neg_view.supports:
        failures.append("negative-premise")

    # ------------------------------------------------------------------ #
    section("4. Multiple goals share sub-proofs, not results")
    kb4 = KnowledgeBase()
    kb4.commit(
        kb4.add_fact("F", "f"),
        kb4.add_rule(Rule("rx", "x", ("f",))),
        kb4.add_rule(Rule("rg", "g", ("x",))),
        kb4.add_rule(Rule("rh", "h", ("x",))),
    )
    vg = kb4.query("g", 100)
    vh = kb4.query("h", 100)
    shared_g = [s.step_id for s in vg.supports[0].steps if s.step_id.startswith("rule:rx")]
    shared_h = [s.step_id for s in vh.supports[0].steps if s.step_id.startswith("rule:rx")]
    print("g:", fmt(vg.supports[0]), " shared x-step:", shared_g)
    print("h:", fmt(vh.supports[0]), " shared x-step:", shared_h)
    print("goal acceptance records are distinct objects:",
          vg.supports[0] is not vh.supports[0])
    if shared_g != shared_h or vg.supports[0] is vh.supports[0]:
        failures.append("shared-subproof")

    # ------------------------------------------------------------------ #
    section("5. Rootless cycles prove nothing")
    kb5 = KnowledgeBase()
    kb5.commit(
        kb5.add_rule(Rule("selfloop", "p", ("p",))),
        kb5.add_rule(Rule("m1", "q", ("r",))),
        kb5.add_rule(Rule("m2", "r", ("q",))),
    )
    p_view = kb5.query("p", 100)
    q_view = kb5.query("q", 100)
    print("p :- p          ->", [fmt(s) for s in p_view.supports],
          f"(complete={p_view.complete})")
    print("q :- r, r :- q  ->", [fmt(s) for s in q_view.supports],
          f"(complete={q_view.complete})")
    if p_view.supports or q_view.supports:
        failures.append("rootless-cycle")

    # ------------------------------------------------------------------ #
    section("6. Lazy enumeration: budget truncation, pause, resume")
    kb6 = KnowledgeBase()
    changes = [kb6.add_fact(f"F{i}", f"f{i}") for i in range(6)]
    changes.append(kb6.add_rule(Rule("rg", "g", (ANY(*[f"f{i}" for i in range(6)]),))))
    kb6.commit(*changes)
    view = kb6.query("g", budget=2)
    print("pull(budget=2):", [fmt(s) for s in view.supports],
          "truncated:", view.last.truncated, "complete:", view.last.complete)
    rounds = 1
    while not view.complete:
        result = view.pull(2)
        rounds += 1
        print(f"resume #{rounds}:   ", [fmt(s) for s in result.supports],
              "truncated:", result.truncated, "complete:", result.complete)
    ids = {s.support_id for s in view.supports}
    if len(ids) != 6 or view.last.truncated:
        failures.append("budget-pause-resume")

    # ------------------------------------------------------------------ #
    section("7. Stable identity/version, replayable evidence, verifier")
    kb7 = KnowledgeBase()
    kb7.commit(
        kb7.add_fact("A", "a", cost=3),
        kb7.add_fact("B", "b", cost=2),
        kb7.add_rule(Rule("rx", "x", ("a",))),
        kb7.add_rule(Rule("rg", "g", ("x", "b"))),
    )
    support = kb7.query("g", 100).supports[0]
    bundle = support.to_bundle(kb7.snapshot())
    result = verify_evidence(bundle, live_kb=kb7.to_json())
    print("support id:", support.support_id, "facts:", fmt(support),
          "cost:", support.cost)
    print("verifier:", result)
    print("derivation order:")
    for step in support.steps:
        print("   ", step.step_id, "=>", step.atom)
    if not result["ok"]:
        failures.append("verifier")

    # A fact version bump changes the step and support ids.
    kb7.commit(kb7.update_fact_version("A", 2))
    bumped = kb7.goal("g").supports[0]
    print("after fact v1->v2 support id:", bumped.support_id)
    if bumped.support_id == support.support_id:
        failures.append("versioning")

    # ------------------------------------------------------------------ #
    section("8. Small graph: exhaustive comparison of all minimal sets")
    universe = {"F1": "a", "F2": "b", "F3": "c", "F4": "d"}
    rules = [
        Rule("r1", "g", ("a", ANY("b", "c"))),
        Rule("r3", "x", ("a", "b")),
        Rule("r4", "g", ("x", "c")),
        Rule("r5", "g", ("d",)),
    ]
    kb8 = KnowledgeBase()
    chg = [kb8.add_fact(fid, atom) for fid, atom in universe.items()]
    chg += [kb8.add_rule(r) for r in rules]
    kb8.commit(*chg)
    engine = {frozenset(s.facts) for s in kb8.query("g", 20000).supports}
    oracle = brute_force_minimal_supports(universe, rules, "g")
    print("engine :", sorted(map(set, engine)))
    print("oracle :", sorted(map(set, oracle)))
    print("match  :", engine == oracle)
    print("inclusion-minimal family :", sorted(map(set, oracle)))
    print("minimum-cardinality only :", [set(x) for x in min_cardinality_supports(oracle)])
    if engine != oracle:
        failures.append("exhaustive")

    # ------------------------------------------------------------------ #
    section("RESULT")
    if failures:
        print("FAIL:", failures)
        return 1
    print("ALL DEMONSTRATION CHECKS PASSED")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
