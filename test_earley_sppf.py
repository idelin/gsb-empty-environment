import math
import unittest

from earley_sppf import Grammar, Production


class EarleySPPFTest(unittest.TestCase):
    def test_left_recursive_ambiguous_aaa_keeps_two_derivations(self):
        grammar = Grammar(
            alphabet=("a",),
            productions=(Production("S", ("S", "S")), Production("S", ("a",))),
            start_symbol="S",
        )

        result = grammar.parse("aaa")

        self.assertTrue(result.accepted)
        self.assertEqual(result.farthest_position, 3)
        self.assertEqual(result.derivation_count(), 2)
        summary = result.forest_summary()
        self.assertGreaterEqual(summary["ambiguous_nodes"], 1)
        self.assertGreater(summary["packed_families"], 1)

    def test_shared_forest_for_long_ambiguous_input_is_not_enumeration(self):
        grammar = Grammar(
            alphabet=("a",),
            productions=(Production("S", ("S", "S")), Production("S", ("a",))),
            start_symbol="S",
        )

        result = grammar.parse("a" * 20)

        self.assertTrue(result.accepted)
        self.assertEqual(result.derivation_count(), 1_767_263_190)
        summary = result.forest_summary()
        self.assertLess(summary["nodes"], 1000)
        self.assertLess(summary["packed_families"], 5000)

    def test_empty_input_accepted_by_epsilon_grammar(self):
        grammar = Grammar(
            alphabet=("a",),
            productions=(Production("S", ()), Production("S", ("a",))),
            start_symbol="S",
        )

        result = grammar.parse(())

        self.assertTrue(result.accepted)
        self.assertEqual(result.farthest_position, 0)
        self.assertEqual(result.derivation_count(), 1)

    def test_empty_input_rejected_without_epsilon(self):
        grammar = Grammar(
            alphabet=("a",),
            productions=(Production("S", ("a",)),),
            start_symbol="S",
        )

        result = grammar.parse(())

        self.assertFalse(result.accepted)
        self.assertEqual(result.farthest_position, 0)
        self.assertIsNone(result.root)

    def test_left_recursive_epsilon_grammar(self):
        grammar = Grammar(
            alphabet=("a",),
            productions=(Production("S", ("S", "a")), Production("S", ())),
            start_symbol="S",
        )

        result = grammar.parse("aa")

        self.assertTrue(result.accepted)
        self.assertEqual(result.derivation_count(), 1)

    def test_cyclic_nullable_grammar_has_infinite_derivations_but_finite_sppf(self):
        grammar = Grammar(
            alphabet=("a",),
            productions=(Production("S", ("S", "S")), Production("S", ())),
            start_symbol="S",
        )

        result = grammar.parse(())

        self.assertTrue(result.accepted)
        self.assertEqual(result.derivation_count(), math.inf)
        self.assertLess(result.forest_summary()["nodes"], 20)

    def test_duplicate_productions_keep_distinct_proof_edges(self):
        grammar = Grammar(
            alphabet=("a",),
            productions=(
                Production("S", ("a",)),
                Production("S", ("a",)),
            ),
            start_symbol="S",
        )

        result = grammar.parse("a")

        self.assertTrue(result.accepted)
        self.assertEqual(result.derivation_count(), 2)
        self.assertEqual(len(result.root.families), 2)

    def test_failure_reports_farthest_prefix(self):
        grammar = Grammar(
            alphabet=("a", "b"),
            productions=(Production("S", ("a", "a", "b")),),
            start_symbol="S",
        )

        result = grammar.parse("aax")

        self.assertFalse(result.accepted)
        self.assertEqual(result.farthest_position, 2)

    def test_unknown_terminal_stops_at_its_own_position(self):
        grammar = Grammar(
            alphabet=("a",),
            productions=(Production("S", ("a", "a")),),
            start_symbol="S",
        )

        result = grammar.parse("xb")

        self.assertFalse(result.accepted)
        self.assertEqual(result.farthest_position, 0)

    def test_invalid_grammar_references_undefined_symbol(self):
        with self.assertRaises(ValueError):
            Grammar(
                alphabet=("a",),
                productions=(Production("S", ("A",)),),
                start_symbol="S",
            )


if __name__ == "__main__":
    unittest.main(verbosity=2)
