"""Runnable synthetic demo:  python3.11 demo.py

Walks through every capability of the support-set maintenance system on
synthetic data: dominance filtering, cost-ordered lazy enumeration with
budgets, retraction recovery, negation, shared subproofs, cycle protection,
pause/resume and independent verification with replayable proofs.
"""

import json

from supportdag import (
    Alt,
    Engine,
    Fact,
    Rule,
    SupportEnumerator,
    fact_lit,
    goal_lit,
    neg_lit,
)
from verifier import Proof, replay_proof, verify_support


def fmt(support):
    pos = ",".join(sorted(f"{f.fact_id}@v{f.version}" for f in support.pos))
    neg = ",".join(sorted(support.neg))
    out = "{" + (pos or "∅") + "}"
    if neg:
        out += f"  requiring ¬{{{neg}}}"
    return out


def show(title):
    print(f"\n=== {title} ===")


def fresh_engine(facts, rules):
    engine = Engine()
    for fid, cost in facts:
        engine.add_fact(Fact(fid, 1, cost))
    for rule in rules:
        engine.put_rule(rule)
    return engine


def main():
    show("1. Inclusion-minimal supports: {A} dominates {A,B}, not {C}")
    engine = fresh_engine(
        [("A", 1.0), ("B", 1.0), ("C", 1.0)],
        [Rule("RG", "G", 1, (
            Alt("a1", (fact_lit("A"),)),
            Alt("a2", (fact_lit("A"), fact_lit("B"))),
            Alt("a3", (fact_lit("C"),)),
        ))],
    )
    for s in engine.supports_of("G"):
        print("  G <=", fmt(s))

    show("2. Cost-ordered lazy enumeration + budget truncation")
    engine = fresh_engine(
        [("A", 3.0), ("B", 1.0), ("C", 2.0)],
        [Rule("RM", "M", 1, (
            Alt("a", (fact_lit("A"),)),
            Alt("b", (fact_lit("B"),)),
            Alt("c", (fact_lit("C"),)),
        ))],
    )
    enum = SupportEnumerator(engine, "M")
    while (s := enum.next()) is not None:
        print("  yield", fmt(s), f"(cost {sum(engine.facts[f.fact_id].cost for f in s.pos)})")
    print("  result:", enum.result().complete and "complete" or "incomplete")
    enum = SupportEnumerator(engine, "M", max_results=2)
    while enum.next() is not None:
        pass
    res = enum.result()
    print(f"  budget=2 -> {len(res.supports)} supports, complete={res.complete} ({res.reason})")

    show("3. Retraction: pruned alternative proof reappears")
    engine = fresh_engine(
        [("A", 1.0), ("C", 1.0), ("D", 1.0)],
        [Rule("RH", "H", 1, (
            Alt("cheap", (fact_lit("A"),)),
            Alt("backup", (fact_lit("C"), fact_lit("D"))),
        ))],
    )
    enum = SupportEnumerator(engine, "H", max_results=1)
    print("  with A, budget=1:", [fmt(s) for s in iter(enum.next, None)],
          "complete =", enum.result().complete)
    engine.retract_fact("A")
    enum = SupportEnumerator(engine, "H")
    print("  A retracted   :", [fmt(s) for s in iter(enum.next, None)],
          "complete =", enum.result().complete)

    show("4. Explicit negation: adding the negated fact kills the support")
    engine = fresh_engine(
        [("X", 1.0)],
        [Rule("RN", "N", 1, (Alt("a1", (fact_lit("X"), neg_lit("Y"))),))],
    )
    print("  Y absent :", [fmt(s) for s in engine.supports_of("N")])
    engine.add_fact(Fact("Y", 1))
    print("  Y added  :", engine.supports_of("N"))
    engine.retract_fact("Y")
    print("  Y removed:", [fmt(s) for s in engine.supports_of("N")])

    show("5. Shared subproofs across goals, no cross-contamination")
    engine = fresh_engine(
        [("A", 1.0), ("B", 1.0), ("C", 1.0)],
        [
            Rule("RS", "S", 1, (Alt("a1", (fact_lit("C"),)),)),
            Rule("RG1", "G1", 1, (Alt("a1", (goal_lit("S"), fact_lit("A"))),)),
            Rule("RG2", "G2", 1, (Alt("a1", (goal_lit("S"), fact_lit("B"))),)),
        ],
    )
    print("  G1:", [fmt(s) for s in engine.supports_of("G1")])
    print("  G2:", [fmt(s) for s in engine.supports_of("G2")])

    show("6. Ungrounded cycles derive nothing; grounded cycles use the base")
    engine = fresh_engine([], [
        Rule("RP", "P", 1, (Alt("a1", (goal_lit("Q"),)),)),
        Rule("RQ", "Q", 1, (Alt("a1", (goal_lit("P"),)),)),
    ])
    print("  pure cycle P<->Q :", engine.supports_of("P"))
    engine.add_fact(Fact("Z", 1))
    engine.put_rule(Rule("RQ", "Q", 2, (
        Alt("loop", (goal_lit("P"),)),
        Alt("base", (fact_lit("Z"),)),
    )))
    print("  after RQ v2      :", [fmt(s) for s in engine.supports_of("P")])

    show("7. Pause / resume with a JSON checkpoint")
    engine = fresh_engine(
        [("A", 3.0), ("B", 1.0), ("C", 2.0)],
        [Rule("RM", "M", 1, (
            Alt("a", (fact_lit("A"),)),
            Alt("b", (fact_lit("B"),)),
            Alt("c", (fact_lit("C"),)),
        ))],
    )
    enum = SupportEnumerator(engine, "M")
    print("  yielded before pause:", fmt(enum.next()))
    checkpoint = json.dumps(enum.checkpoint())
    print("  checkpoint:", checkpoint[:96] + "...")
    resumed = SupportEnumerator.resume(engine, json.loads(checkpoint))
    print("  after resume        :", [fmt(s) for s in iter(resumed.next, None)])

    show("8. Independent verification + replayable proof")
    engine = fresh_engine(
        [("A", 1.0), ("B", 1.0), ("C", 1.0), ("D", 1.0)],
        [
            Rule("R1", "G1", 1, (
                Alt("a1", (fact_lit("A"), fact_lit("B"))),
                Alt("a2", (fact_lit("C"), neg_lit("E"))),
            )),
            Rule("R2", "G2", 1, (
                Alt("a1", (goal_lit("G1"), fact_lit("D"))),
                Alt("a2", (fact_lit("A"), fact_lit("D"))),
            )),
        ],
    )
    reports = [(s, verify_support(engine, "G2", s)) for s in engine.supports_of("G2")]
    for s, report in reports:
        print(f"  verify {fmt(s)} -> ok={report.ok} {report.reasons or ''}")
    proof = next(r.proof for s, r in reports if s.neg)
    print("  proof steps for the support requiring ¬E:")
    for step in proof.steps.values():
        print(f"    {step.step_id}  {step.kind:<7} {step.ref} v{step.version}"
              f"  children={list(step.children)}")
    print("  replay against current evidence:", replay_proof(engine, proof))
    engine.add_fact(Fact("D", 2, 1.0))
    print("  replay after D bumped to v2   :", replay_proof(engine, proof))


if __name__ == "__main__":
    main()
