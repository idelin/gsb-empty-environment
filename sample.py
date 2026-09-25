"""Runnable synthetic sample for the proofdag package.

Scenario (synthetic data): two sensor readings and a humidity fact are
combined by a commutative conjunction rule; ordered modus-ponens rules
derive an alert and an evacuation.  A shared subproof (the conjunction)
feeds two parents, and a structurally identical duplicate step is folded
into one canonical node.

Run:  python3.11 sample.py
"""

from __future__ import annotations

from proofdag import (
    CycleError,
    Fact,
    ProofDAG,
    Rule,
    Step,
    canonicalize,
    export_evidence,
    summaries_equal,
    verify,
)

CONJ = Rule(stable_id="rule-conj", version=1, name="and-intro", commutative=True)
MP = Rule(stable_id="rule-mp", version=1, name="modus-ponens", commutative=False)


def build_dag(rename=lambda nid: nid, permute=False, source_b="sensor-B"):
    reading_a = Fact("fact-temp-high", 1, "sensor-A", "temp>30")
    reading_b = Fact("fact-temp-high", 1, source_b, "temp>30")  # same value, other source
    humid = Fact("fact-humid-high", 1, "sensor-A", "humidity>80")
    imp_alert = Fact("fact-imp-alert", 1, "kb", "(temp>30 & humidity>80) -> alert")
    imp_evac = Fact("fact-imp-evac", 1, "kb", "alert -> evacuate")
    imp_log = Fact("fact-imp-log", 1, "kb", "(temp>30 & humidity>80) -> log-entry")

    dag = ProofDAG()
    for fact in (reading_a, reading_b, humid, imp_alert, imp_evac, imp_log):
        dag.add_fact(fact)

    def conclusion(stable_id, payload):
        return Fact(stable_id, 1, "proof", payload)

    steps = [
        Step(rename("s1"), "step-and-1", 1, CONJ,
             (reading_a.node_id, humid.node_id),
             conclusion("fact-and-1", "temp>30 & humidity>80")),
        # structurally identical duplicate (swapped conjunctive premises,
        # different internal id and stable id) -> deduplicated
        Step(rename("s1-dup"), "step-and-1-copy", 1, CONJ,
             (humid.node_id, reading_a.node_id),
             conclusion("fact-and-1", "temp>30 & humidity>80")),
        Step(rename("s2"), "step-alert", 1, MP,
             (rename("s1"), imp_alert.node_id),
             conclusion("fact-alert", "alert")),
        Step(rename("s3"), "step-evac", 1, MP,
             (rename("s2"), imp_evac.node_id),
             conclusion("fact-evac", "evacuate")),
        # second parent referencing the duplicate of the shared subproof
        Step(rename("s4"), "step-log", 1, MP,
             (rename("s1-dup"), imp_log.node_id),
             conclusion("fact-log", "log-entry")),
    ]
    if permute:
        steps = [steps[3], steps[0], steps[4], steps[2], steps[1]]
    for step in steps:
        dag.add_step(step)
    dag.add_goal(rename("s3"))
    dag.add_goal(rename("s4"))
    dag.add_goal(reading_b.node_id)  # same value as reading_a, other source
    return dag


def show(proof):
    print(f"{'idx':>3} {'kind':<4} {'children':<10} {'parents':<8} "
          f"{'goal':<4} identity")
    for node in proof.nodes:
        if node.kind == "F":
            _, sid, ver, src, payload = node.key
            ident = f"fact {sid} v{ver} src={src} payload={payload!r}"
        else:
            _, rid, rver, _ = node.key
            ident = f"step rule={rid} v{rver} ids={list(node.step_ids)}"
        children = ",".join(map(str, node.children))
        parents = ",".join(map(str, node.parents))
        goal = "*" if node.is_goal else ""
        print(f"{node.index:>3} {node.kind:<4} {children:<10} {parents:<8} "
              f"{goal:<4} {ident}")
    print(f"summary: {proof.summary}")


def main():
    print("== canonical proof ==")
    proof = canonicalize(build_dag())
    show(proof)

    print("\n== invariance: renamed internal nodes + permuted inputs ==")
    renamed = canonicalize(build_dag(rename=lambda nid: "node-" + nid,
                                     permute=True))
    print(f"same summary: {summaries_equal(proof, renamed)}")

    print("\n== source substitution must change the result ==")
    substituted = canonicalize(build_dag(source_b="sensor-C"))
    print(f"same summary: {summaries_equal(proof, substituted)}")

    print("\n== cyclic proof rejected ==")
    cyclic = ProofDAG()
    cyclic.add_step(Step("x", "step-x", 1, MP, ("y",),
                         Fact("fact-x", 1, "proof", "x")))
    cyclic.add_step(Step("y", "step-y", 1, MP, ("x",),
                         Fact("fact-y", 1, "proof", "y")))
    cyclic.add_goal("x")
    try:
        canonicalize(cyclic)
        print("NOT rejected (bug)")
    except CycleError as exc:
        print(f"rejected: {exc}")

    print("\n== incremental edge deletion revokes unreachable shared subgraph ==")
    dag = build_dag()
    revoked = dag.remove_edge("s4", "s1-dup")
    print(f"remove s4->s1-dup, revoked: {revoked} (s1 survives via s2)")
    revoked = dag.remove_edge("s2", "s1")
    print(f"remove s2->s1, revoked: {revoked}")

    print("\n== replayable evidence ==")
    evidence = export_evidence(build_dag(), proof)
    print(f"verify(replay(evidence)): {verify(evidence)}")
    tampered = dict(evidence, summary="0" * 64)
    print(f"verify(tampered evidence): {verify(tampered)}")


if __name__ == "__main__":
    main()
