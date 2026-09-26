"""Infinitude detection: evidence-based, irrelevant loops must not count."""

import unittest

from automata import (Automaton, ProductAutomaton, find_infinite_evidence,
                      verify_evidence)


def single(alphabet, states, initial, accepting, transitions):
    return ProductAutomaton([Automaton(alphabet, states, initial,
                                       accepting, transitions)])


class TestInfiniteEvidence(unittest.TestCase):
    def test_simple_loop_gives_verifiable_evidence(self):
        # language a*b : infinite
        product = single("ab", [0, 1], 0, {1},
                         {(0, "a"): 0, (0, "b"): 1})
        ev = find_infinite_evidence(product)
        self.assertIsNotNone(ev)
        self.assertNotEqual(ev.loop, "")
        self.assertTrue(verify_evidence(product, ev, repeats=range(0, 8)))
        for i in range(8):
            self.assertTrue(product.accepts(ev.witness(i)))

    def test_intersection_loop(self):
        # even number of a's  AND  ends with b -> infinite intersection
        even_a = Automaton("ab", ["even", "odd"], "even", {"even"},
                           {("even", "a"): "odd", ("odd", "a"): "even",
                            ("even", "b"): "even", ("odd", "b"): "odd"})
        ends_b = Automaton("ab", ["nb", "b"], "nb", {"b"},
                           {("nb", "a"): "nb", ("nb", "b"): "b",
                            ("b", "a"): "nb", ("b", "b"): "b"})
        product = ProductAutomaton([even_a, ends_b])
        ev = find_infinite_evidence(product)
        self.assertIsNotNone(ev)
        self.assertTrue(verify_evidence(product, ev, repeats=range(0, 6)))

    def test_irrelevant_cycle_does_not_imply_infinite(self):
        # 'b'-loop is a dead end; language is exactly {'a'} -> finite
        product = single("ab", [0, 1, 2], 0, {1},
                         {(0, "a"): 1, (0, "b"): 2, (2, "b"): 2})
        self.assertIsNone(find_infinite_evidence(product))

    def test_cycle_after_accepting_state_is_irrelevant(self):
        # q1 accepts, but the only cycle (q2) cannot return to acceptance
        product = single("a", [0, 1, 2], 0, {1},
                         {(0, "a"): 1, (1, "a"): 2, (2, "a"): 2})
        self.assertIsNone(find_infinite_evidence(product))

    def test_empty_language_is_finite(self):
        product = single("a", [0, 1], 0, {1}, {(0, "a"): 0})
        self.assertIsNone(find_infinite_evidence(product))

    def test_finite_language(self):
        product = single("a", [0, 1, 2], 0, {2},
                         {(0, "a"): 1, (1, "a"): 2})
        self.assertIsNone(find_infinite_evidence(product))

    def test_evidence_is_independently_executable(self):
        # verify_evidence only needs the automaton interface, no Counter
        product = single("ab", [0, 1], 0, {1},
                         {(0, "a"): 0, (0, "b"): 1, (1, "a"): 1})
        ev = find_infinite_evidence(product)
        self.assertTrue(verify_evidence(product, ev))
        # tampered evidence must fail
        bad = ev._replace(loop=ev.loop + "b")
        self.assertFalse(verify_evidence(product, bad))


if __name__ == "__main__":
    unittest.main()
