import unittest

from proofdag.errors import UnknownNodeError
from proofdag.model import Fact, Rule, Step
from proofdag.store import ProofStore

CONJ = Rule("conj-intro", 1, commutative=True)
CHAIN = Rule("ordered-chain", 1, commutative=False)


def fact(fid, payload, source="s0"):
    return Fact(fid, 1, source, payload)


def build_shared_store():
    """q1 = chain(p, c), q2 = chain(c, p) share subproof p = conj(a, b)."""
    store = ProofStore()
    a = fact("a", "A")
    b = fact("b", "B")
    c = fact("c", "C")
    p = Step("p", 1, CONJ, (a, b), fact("p", "A ∧ B"))
    q1 = Step("q1", 1, CHAIN, (p, c), fact("q1", "A ∧ B⇒C"))
    q2 = Step("q2", 1, CHAIN, (c, p), fact("q2", "C⇒A ∧ B"))
    keys = {
        "a": store.add_fact(a),
        "b": store.add_fact(b),
        "c": store.add_fact(c),
        "p": store.add_step(p),
        "q1": store.add_step(q1),
        "q2": store.add_step(q2),
    }
    store.mark_root(keys["q1"])
    store.mark_root(keys["q2"])
    return store, keys


class StructuralSharingTests(unittest.TestCase):
    def test_shared_subproof_stored_once(self):
        store, keys = build_shared_store()
        self.assertEqual(store.node_count(), 6)  # a, b, c, p, q1, q2
        again = store.add_step(
            Step("p-renamed", 2, CONJ,
                 (fact("a", "A"), fact("b", "B")), fact("p", "A ∧ B"))
        )
        self.assertEqual(again, keys["p"])
        self.assertEqual(store.node_count(), 6)

    def test_back_references_complete(self):
        store, keys = build_shared_store()
        self.assertEqual(store.parents_of(keys["p"]), {keys["q1"], keys["q2"]})
        self.assertEqual(store.parents_of(keys["c"]), {keys["q1"], keys["q2"]})
        self.assertEqual(store.parents_of(keys["a"]), {keys["p"]})

    def test_stable_step_ids_resolve_to_shared_key(self):
        store, keys = build_shared_store()
        self.assertEqual(store.step_index["p"], keys["p"])
        self.assertEqual(store.step_index["q1"], keys["q1"])
        # A renamed but structurally identical step maps to the same key.
        store.add_step(Step("p2", 1, CONJ,
                            (fact("a", "A"), fact("b", "B")), fact("p", "A ∧ B")))
        self.assertEqual(store.step_index["p2"], keys["p"])

    def test_facts_rules_steps_carry_ids_and_versions(self):
        store, keys = build_shared_store()
        evidence = store.evidence()
        for node in evidence["nodes"]:
            self.assertIn("fact_id", node["fact"])
            self.assertIn("version", node["fact"])
            if node["kind"] == "derived":
                self.assertIn("rule_id", node["rule"])
                self.assertIn("version", node["rule"])
        self.assertIn("p", store.step_index)


class IncrementalDeletionTests(unittest.TestCase):
    def test_edge_deletion_keeps_still_reachable_shared_subgraph(self):
        store, keys = build_shared_store()
        store.remove_edge(keys["q1"], keys["p"])
        self.assertTrue(store.has_node(keys["p"]))   # still used by q2
        self.assertTrue(store.has_node(keys["a"]))
        self.assertEqual(store.parents_of(keys["p"]), {keys["q2"]})

    def test_last_edge_deletion_revokes_unreachable_subgraph(self):
        store, keys = build_shared_store()
        store.remove_edge(keys["q1"], keys["p"])
        store.remove_edge(keys["q2"], keys["p"])
        # p is now unreachable from roots; its private leaves a, b go too.
        for name in ("p", "a", "b"):
            self.assertFalse(store.has_node(keys[name]), name)
        # c is still referenced by both roots.
        self.assertTrue(store.has_node(keys["c"]))
        self.assertTrue(store.has_node(keys["q1"]))
        self.assertEqual(store.node_count(), 3)

    def test_root_removal_cascades(self):
        store, keys = build_shared_store()
        store.remove_root(keys["q1"])
        self.assertFalse(store.has_node(keys["q1"]))
        self.assertTrue(store.has_node(keys["q2"]))
        self.assertTrue(store.has_node(keys["p"]))  # shared with q2
        store.remove_root(keys["q2"])
        self.assertEqual(store.node_count(), 0)

    def test_remove_unknown_edge_rejected(self):
        store, keys = build_shared_store()
        with self.assertRaises(UnknownNodeError):
            store.remove_edge(keys["q1"], keys["a"])
        with self.assertRaises(UnknownNodeError):
            store.remove_edge(("nope",), keys["a"])


if __name__ == "__main__":
    unittest.main()
