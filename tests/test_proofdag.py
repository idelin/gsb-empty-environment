import unittest

from proofdag import (
    CycleError,
    Fact,
    ProofDAG,
    Rule,
    Step,
    UnknownNodeError,
    canonicalize,
    export_evidence,
    replay,
    summaries_equal,
    verify,
)

CONJ = Rule("rule-conj", 1, "and-intro", commutative=True)
MP = Rule("rule-mp", 1, "modus-ponens", commutative=False)


def fact(sid, src="src-A", payload=None, version=1):
    return Fact(sid, version, src, payload if payload is not None else sid)


def step(nid, sid, rule, premises, version=1):
    return Step(nid, sid, version, rule, tuple(premises),
                Fact("c-" + sid, 1, "proof", "derived-" + sid))


def build(rename=lambda x: x, order=None, source_b="src-B"):
    """Shared subproof s1 referenced by s2 and s4; s1-dup is a structural
    duplicate of s1 with swapped conjunctive premises."""
    a = fact("fact-a", "src-A", "P")
    b = fact("fact-b", "src-A", "Q")
    a_other = fact("fact-a", source_b, "P")  # same value, other source
    i1 = fact("fact-i1", "kb", "PQ->R")
    i2 = fact("fact-i2", "kb", "PQ->S")
    dag = ProofDAG()
    for f in (a, b, a_other, i1, i2):
        dag.add_fact(f)
    steps = {
        "s1": step(rename("s1"), "step-and", CONJ, [a.node_id, b.node_id]),
        "s1-dup": step(rename("s1-dup"), "step-and-copy", CONJ,
                       [b.node_id, a.node_id]),
        "s2": step(rename("s2"), "step-r", MP, [rename("s1"), i1.node_id]),
        "s4": step(rename("s4"), "step-s", MP, [rename("s1-dup"), i2.node_id]),
    }
    for nid in (order or ["s1", "s1-dup", "s2", "s4"]):
        dag.add_step(steps[nid])
    dag.add_goal(rename("s2"))
    dag.add_goal(rename("s4"))
    dag.add_goal(a_other.node_id)
    return dag


class TestCanonicalization(unittest.TestCase):
    def test_structural_dedup(self):
        proof = canonicalize(build())
        step_nodes = [n for n in proof.nodes if n.kind == "S"]
        # s1 and s1-dup merge: 4 step nodes -> 3 canonical step nodes
        self.assertEqual(len(step_nodes), 3)
        merged = [n for n in step_nodes if len(n.step_ids) == 2]
        self.assertEqual(len(merged), 1)
        self.assertEqual(sorted(merged[0].step_ids),
                         [("step-and", 1), ("step-and-copy", 1)])

    def test_summary_invariant_to_input_permutation(self):
        base = canonicalize(build())
        permuted = canonicalize(build(order=["s4", "s1-dup", "s2", "s1"]))
        self.assertTrue(summaries_equal(base, permuted))
        self.assertEqual(base.canonical_form, permuted.canonical_form)

    def test_summary_invariant_to_internal_renaming(self):
        base = canonicalize(build())
        renamed = canonicalize(build(rename=lambda x: "zzz-" + x))
        self.assertTrue(summaries_equal(base, renamed))
        self.assertEqual(base.canonical_form, renamed.canonical_form)

    def test_source_substitution_changes_summary(self):
        base = canonicalize(build())
        substituted = canonicalize(build(source_b="src-C"))
        self.assertFalse(summaries_equal(base, substituted))

    def test_same_value_different_source_not_merged(self):
        dag = build()
        proof = canonicalize(dag)
        payloads = [n.key for n in proof.nodes if n.kind == "F"]
        same_value = [k for k in payloads if k[4] == "P"]
        # fact-a from src-A and fact-a from src-B stay distinct
        self.assertEqual(len(same_value), 2)
        self.assertEqual({k[3] for k in same_value}, {"src-A", "src-B"})

    def test_commutative_premise_swap_same_summary(self):
        dag1 = ProofDAG()
        a, b = fact("fa", payload="P"), fact("fb", payload="Q")
        dag1.add_fact(a)
        dag1.add_fact(b)
        dag1.add_step(step("x", "sx", CONJ, [a.node_id, b.node_id]))
        dag1.add_goal("x")
        dag2 = ProofDAG()
        dag2.add_fact(a)
        dag2.add_fact(b)
        dag2.add_step(step("x", "sx", CONJ, [b.node_id, a.node_id]))
        dag2.add_goal("x")
        self.assertTrue(summaries_equal(canonicalize(dag1), canonicalize(dag2)))

    def test_ordered_premise_swap_not_merged(self):
        a, b = fact("fa", payload="P"), fact("fb", payload="Q")
        dag = ProofDAG()
        dag.add_fact(a)
        dag.add_fact(b)
        dag.add_step(step("xy", "s-ordered-1", MP, [a.node_id, b.node_id]))
        dag.add_step(step("yx", "s-ordered-2", MP, [b.node_id, a.node_id]))
        dag.add_goal("xy")
        dag.add_goal("yx")
        proof = canonicalize(dag)
        step_nodes = [n for n in proof.nodes if n.kind == "S"]
        # ordered rule: swapped premises are NOT merged
        self.assertEqual(len(step_nodes), 2)
        # and the summary differs from a single-order proof
        dag1 = ProofDAG()
        dag1.add_fact(a)
        dag1.add_fact(b)
        dag1.add_step(step("xy", "s-ordered-1", MP, [a.node_id, b.node_id]))
        dag1.add_goal("xy")
        self.assertFalse(summaries_equal(proof, canonicalize(dag1)))

    def test_mixed_commutative_and_ordered_coexist(self):
        proof = canonicalize(build())
        kinds = {n.key[1] for n in proof.nodes if n.kind == "S"}
        self.assertEqual(kinds, {"rule-conj", "rule-mp"})

    def test_cycle_rejected(self):
        dag = ProofDAG()
        dag.add_step(step("x", "sx", MP, ["y"]))
        dag.add_step(step("y", "sy", MP, ["x"]))
        dag.add_goal("x")
        with self.assertRaises(CycleError):
            canonicalize(dag)

    def test_self_loop_rejected(self):
        dag = ProofDAG()
        dag.add_step(step("x", "sx", MP, ["x"]))
        dag.add_goal("x")
        with self.assertRaises(CycleError):
            canonicalize(dag)

    def test_unknown_premise_rejected(self):
        dag = ProofDAG()
        dag.add_step(step("x", "sx", MP, ["missing"]))
        dag.add_goal("x")
        with self.assertRaises(UnknownNodeError):
            canonicalize(dag)

    def test_forced_collision_structural_verification(self):
        collide = lambda data: "CONSTANT"  # noqa: E731
        proof_a = canonicalize(build(), hash_fn=collide)
        proof_b = canonicalize(build(source_b="src-C"), hash_fn=collide)
        proof_a2 = canonicalize(build(rename=lambda x: "r-" + x),
                                hash_fn=collide)
        # hashes collide for all three; structure decides equality
        self.assertEqual(proof_a.summary, proof_b.summary)
        self.assertFalse(summaries_equal(proof_a, proof_b))
        self.assertTrue(summaries_equal(proof_a, proof_a2))

    def test_version_bump_changes_summary(self):
        dag1 = ProofDAG()
        dag1.add_fact(fact("fa", payload="P", version=1))
        dag1.add_goal(fact("fa", payload="P", version=1).node_id)
        dag2 = ProofDAG()
        dag2.add_fact(fact("fa", payload="P", version=2))
        dag2.add_goal(fact("fa", payload="P", version=2).node_id)
        self.assertFalse(summaries_equal(canonicalize(dag1),
                                         canonicalize(dag2)))


class TestSharingAndDeletion(unittest.TestCase):
    def test_shared_subproof_stored_once_with_backrefs(self):
        proof = canonicalize(build())
        shared = [n for n in proof.nodes
                  if n.kind == "S" and ("step-and", 1) in n.step_ids]
        self.assertEqual(len(shared), 1)  # stored once
        parents = shared[0].parents
        self.assertEqual(len(parents), 2)  # referenced by s2 and s4
        parent_rules = {proof.nodes[i].key[1] for i in parents}
        self.assertEqual(parent_rules, {"rule-mp"})
        # every step node lists its premises as children (forward refs)
        for node in proof.nodes:
            for child in node.children:
                self.assertIn(node.index, proof.nodes[child].parents)

    def test_incremental_edge_deletion_revokes_unreachable(self):
        dag = build()
        before = canonicalize(dag)
        revoked = dag.remove_edge("s4", "s1-dup")
        # s1-dup loses its last referrer and is revoked; its structural
        # sibling s1 survives because s2 still references it.
        self.assertEqual(revoked, ["s1-dup"])
        self.assertIn("s1", dag.steps)
        self.assertEqual(dag.parents["s1"], {"s2"})
        revoked = dag.remove_edge("s2", "s1")
        self.assertIn("s1", revoked)  # shared subgraph revoked
        self.assertNotIn("s1", dag.steps)
        after = canonicalize(dag)
        self.assertFalse(summaries_equal(before, after))

    def test_remove_step_revokes_dependents_only(self):
        dag = build()
        revoked = dag.remove_step("s4")
        self.assertIn("s4", revoked)
        self.assertIn("s1-dup", revoked)
        self.assertIn("s1", dag.steps)  # still used by s2
        self.assertEqual(dag.parents["s1"], {"s2"})


class TestEvidence(unittest.TestCase):
    def test_evidence_replay_roundtrip(self):
        dag = build()
        proof = canonicalize(dag)
        evidence = export_evidence(dag, proof)
        self.assertTrue(verify(evidence))
        replayed = replay(evidence)
        self.assertTrue(summaries_equal(proof, replayed))

    def test_evidence_contains_stable_ids_and_versions(self):
        evidence = export_evidence(build(), canonicalize(build()))
        for f in evidence["facts"]:
            self.assertIn("stable_id", f)
            self.assertIn("version", f)
        for r in evidence["rules"]:
            self.assertIn("stable_id", r)
            self.assertIn("version", r)
        for s in evidence["steps"]:
            self.assertIn("stable_id", s)
            self.assertIn("version", s)

    def test_tampered_evidence_rejected(self):
        dag = build()
        evidence = export_evidence(dag, canonicalize(dag))
        tampered_summary = dict(evidence, summary="0" * 64)
        self.assertFalse(verify(tampered_summary))
        tampered_steps = dict(evidence)
        tampered_steps["steps"] = [dict(s) for s in evidence["steps"]]
        tampered_steps["steps"][0]["premises"] = []
        self.assertFalse(verify(tampered_steps))

    def test_evidence_is_json_serializable(self):
        import json

        evidence = export_evidence(build(), canonicalize(build()))
        self.assertTrue(verify(json.loads(json.dumps(evidence))))


if __name__ == "__main__":
    unittest.main()
