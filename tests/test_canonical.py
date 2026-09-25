import unittest

from proofdag.canonical import DigestRegistry, canonicalize
from proofdag.errors import CycleError, UnknownNodeError
from proofdag.model import Fact, Rule, Step
from proofdag.store import ProofStore

CONJ = Rule("conj-intro", 1, commutative=True)
CHAIN = Rule("ordered-chain", 1, commutative=False)


def fact(fid="f", version=1, source="s0", payload="p"):
    return Fact(fid, version, source, payload)


def conj_proof(store, a, b, step_id="s-conj"):
    return store.add_step(
        Step(step_id, 1, CONJ, (a, b), fact("c", source="derived", payload="A ∧ B"))
    )


class CommutativePremiseTests(unittest.TestCase):
    def test_swapped_conjunctive_premises_same_digest(self):
        a = fact("a", payload="A")
        b = fact("b", payload="B")
        s1, s2 = ProofStore(), ProofStore()
        k1 = conj_proof(s1, a, b, "s1")
        k2 = conj_proof(s2, b, a, "s2")
        s1.mark_root(k1)
        s2.mark_root(k2)
        self.assertEqual(k1, k2)
        self.assertEqual(s1.canonical_form().digest, s2.canonical_form().digest)

    def test_ordered_premises_are_not_merged(self):
        a = fact("a", payload="A")
        b = fact("b", payload="B")
        s1, s2 = ProofStore(), ProofStore()
        k1 = s1.add_step(Step("x", 1, CHAIN, (a, b), fact("c", payload="A⇒B")))
        k2 = s2.add_step(Step("x", 1, CHAIN, (b, a), fact("c", payload="A⇒B")))
        s1.mark_root(k1)
        s2.mark_root(k2)
        self.assertNotEqual(k1, k2)
        self.assertNotEqual(s1.canonical_form().digest, s2.canonical_form().digest)

    def test_mixed_commutative_and_ordered_coexist(self):
        a = fact("a", payload="A")
        b = fact("b", payload="B")
        s1, s2 = ProofStore(), ProofStore()
        for store, order in ((s1, (a, b)), (s2, (b, a))):
            conj = Step("c", 1, CONJ, order, fact("cj", payload="A ∧ B"))
            chain = Step("k", 1, CHAIN, (conj, a), fact("ch", payload="A ∧ B⇒A"))
            key = store.add_step(chain)
            store.mark_root(key)
        self.assertEqual(s1.canonical_form().digest, s2.canonical_form().digest)


class NamingAndOrderIndependenceTests(unittest.TestCase):
    def _build(self, names, order):
        store = ProofStore()
        facts = {
            "a": fact("a", payload="A"),
            "b": fact("b", payload="B"),
            "c": fact("c", payload="C"),
        }
        steps = {
            "p": lambda: Step(names["p"], 1, CONJ, (facts["a"], facts["b"]),
                              fact("p", payload="A ∧ B")),
            "q": lambda: Step(names["q"], 1, CHAIN, (facts["c"],),
                              fact("q", payload="C⇒C")),
            "r": lambda: Step(names["r"], 1, CHAIN,
                              (steps["p"](), steps["q"]()),
                              fact("r", payload="A ∧ B⇒C⇒C")),
        }
        keys = {}
        for name in order:
            keys[name] = store.add_step(steps[name]())
        store.mark_root(keys["r"])
        return store

    def test_internal_node_renaming_same_result(self):
        s1 = self._build({"p": "p1", "q": "q1", "r": "r1"}, ("p", "q", "r"))
        s2 = self._build({"p": "zz", "q": "yy", "r": "xx"}, ("p", "q", "r"))
        self.assertEqual(s1.canonical_form(), s2.canonical_form())

    def test_input_permutation_same_result(self):
        s1 = self._build({"p": "p", "q": "q", "r": "r"}, ("p", "q", "r"))
        s2 = self._build({"p": "p", "q": "q", "r": "r"}, ("q", "r", "p"))
        f1, f2 = s1.canonical_form(), s2.canonical_form()
        self.assertEqual(f1.digest, f2.digest)
        self.assertEqual(f1.structural_tuple(), f2.structural_tuple())

    def test_canonicalize_independent_of_graph_enumeration_order(self):
        a, b = fact("a", payload="A"), fact("b", payload="B")
        graph1 = {
            "x": (a, None, ()),
            "y": (b, None, ()),
            "z": (fact("c", payload="A ∧ B"), CONJ, ("x", "y")),
        }
        graph2 = {
            "z": (fact("c", payload="A ∧ B"), CONJ, ("y", "x")),
            "y": (b, None, ()),
            "x": (a, None, ()),
        }
        self.assertEqual(
            canonicalize(graph1, ("z",)).digest,
            canonicalize(graph2, ("z",)).digest,
        )


class SourceIdentityTests(unittest.TestCase):
    def test_source_replacement_changes_result(self):
        s1, s2 = ProofStore(), ProofStore()
        for store, source in ((s1, "sensor-alpha"), (s2, "sensor-beta")):
            key = store.add_fact(fact("a", source=source, payload="A"))
            store.mark_root(key)
        self.assertNotEqual(s1.canonical_form().digest, s2.canonical_form().digest)

    def test_same_value_different_source_not_merged(self):
        store = ProofStore()
        k1 = store.add_fact(fact("a", source="s1", payload="A"))
        k2 = store.add_fact(fact("a", source="s2", payload="A"))
        self.assertNotEqual(k1, k2)
        self.assertEqual(store.node_count(), 2)
        store.mark_root(k1)
        store.mark_root(k2)
        form = store.canonical_form()
        self.assertEqual(len(form.nodes), 2)

    def test_same_source_same_value_merged(self):
        store = ProofStore()
        k1 = store.add_fact(fact("a", source="s1", payload="A"))
        k2 = store.add_fact(fact("a", source="s1", payload="A"))
        self.assertEqual(k1, k2)
        self.assertEqual(store.node_count(), 1)


class CycleRejectionTests(unittest.TestCase):
    def test_cyclic_graph_rejected(self):
        graph = {
            0: (fact("a"), CHAIN, (1,)),
            1: (fact("b"), CHAIN, (0,)),
        }
        with self.assertRaises(CycleError):
            canonicalize(graph, (0,))

    def test_self_loop_rejected(self):
        graph = {0: (fact("a"), CHAIN, (0,))}
        with self.assertRaises(CycleError):
            canonicalize(graph, (0,))

    def test_unknown_reference_rejected(self):
        graph = {0: (fact("a"), CHAIN, (99,))}
        with self.assertRaises(UnknownNodeError):
            canonicalize(graph, (0,))
        with self.assertRaises(UnknownNodeError):
            canonicalize({}, (0,))


class DigestCollisionTests(unittest.TestCase):
    def test_forced_collision_verified_by_structure(self):
        collide = lambda text: "deadbeef"
        s1, s2 = ProofStore(digest_fn=collide), ProofStore(digest_fn=collide)
        k1 = s1.add_fact(fact("a", source="s1", payload="A"))
        k2 = s2.add_fact(fact("b", source="s1", payload="B"))
        s1.mark_root(k1)
        s2.mark_root(k2)
        f1, f2 = s1.canonical_form(), s2.canonical_form()
        self.assertEqual(f1.digest, f2.digest)
        self.assertNotEqual(f1.structural_tuple(), f2.structural_tuple())

        registry = DigestRegistry()
        _, created1 = registry.register(f1)
        _, created2 = registry.register(f2)
        self.assertTrue(created1)
        self.assertTrue(created2)
        self.assertEqual(registry.bucket_size("deadbeef"), 2)

        again, created3 = registry.register(f1)
        self.assertFalse(created3)
        self.assertIs(again, f1)
        self.assertIs(registry.resolve(f1), f1)
        self.assertIs(registry.resolve(f2), f2)


if __name__ == "__main__":
    unittest.main()
