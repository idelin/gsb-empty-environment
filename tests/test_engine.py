import unittest

from evidence_engine import BudgetExceeded, Engine, Rule


class AcceptanceTests(unittest.TestCase):
    """The exact acceptance boundary from the specification."""

    def _cycle_engine(self):
        return Engine([Rule(["A"], "B"), Rule(["B"], "A")])

    def test_revoking_only_source_removes_cycle(self):
        engine = self._cycle_engine()
        engine.add_source("s", "A")
        self.assertEqual(engine.all_facts(), ["A", "B"])
        self.assertEqual(engine.supports("A"), [frozenset({"s"})])
        self.assertEqual(engine.supports("B"), [frozenset({"s"})])

        engine.revoke_source("s")
        self.assertEqual(engine.all_facts(), [])
        self.assertFalse(engine.supported("A"))
        self.assertFalse(engine.supported("B"))

    def test_independent_source_keeps_both_after_revoke(self):
        engine = self._cycle_engine()
        engine.add_source("s", "A")
        engine.add_source("t", "B")
        engine.revoke_source("s")
        self.assertEqual(engine.all_facts(), ["A", "B"])
        self.assertEqual(engine.supports("B"), [frozenset({"t"})])
        self.assertEqual(engine.supports("A"), [frozenset({"t"})])

    def test_empty_cycle_never_bootstraps(self):
        engine = self._cycle_engine()
        self.assertEqual(engine.all_facts(), [])
        self.assertIsNone(engine.trace("A"))


class SourceSemanticsTests(unittest.TestCase):
    def test_independent_sources_are_separate_minimal_supports(self):
        engine = Engine([Rule(["A"], "B")])
        engine.add_source("s", "A")
        engine.add_source("t", "A")
        self.assertEqual(
            engine.supports("A"), [frozenset({"s"}), frozenset({"t"})]
        )
        self.assertEqual(
            engine.supports("B"), [frozenset({"s"}), frozenset({"t"})]
        )

    def test_union_support_for_two_premise_rule(self):
        engine = Engine([Rule(["A", "C"], "B")])
        engine.add_source("s", "A")
        engine.add_source("t", "C")
        self.assertEqual(engine.supports("B"), [frozenset({"s", "t"})])
        # An independent source on C yields a second, incomparable support.
        engine.add_source("u", "C")
        self.assertEqual(
            engine.supports("B"),
            [frozenset({"s", "t"}), frozenset({"s", "u"})],
        )

    def test_ordering_cardinality_then_lexicographic(self):
        # Rule C := A, B. A is backed by {a} and {z}; B by {a} and {b}.
        # Unions: {a} (A's {a} with B's {a}) and {a,b}, {a,z}, {b,z}.
        # Only {a} and {b,z} are inclusion-minimal; size first, then lex.
        engine = Engine([Rule(["A", "B"], "C")])
        engine.add_source("z", "A")
        engine.add_source("a", "A")
        engine.add_source("a", "B")
        engine.add_source("b", "B")
        supports = [sorted(support) for support in engine.supports("C")]
        self.assertEqual(supports, [["a"], ["b", "z"]])

    def test_dominated_support_sets_are_discarded(self):
        engine = Engine([Rule(["A"], "B"), Rule(["B", "C"], "D")])
        engine.add_source("s", "A")
        engine.add_source("s", "C")  # same source covers both premises
        engine.add_source("t", "C")
        # B via s only -> D with {s}; {s,t} is dominated and must not appear.
        self.assertEqual(engine.supports("D"), [frozenset({"s"})])

    def test_cross_cutting_source_alternative_is_kept(self):
        # A premise may need to keep a cardinality-tied alternative whose
        # source ids overlap differently with other premises.
        engine = Engine([
            Rule(["X", "Y"], "Z"),
            Rule(["P"], "X"),
            Rule(["Q"], "X"),
        ])
        engine.add_source("p", "P")
        engine.add_source("q", "Q")
        engine.add_source("p", "Y")
        engine.add_source("q", "Y")
        self.assertEqual(
            engine.supports("Z"),
            [frozenset({"p"}), frozenset({"q"})],
        )

    def test_revoke_one_of_several_sources_keeps_derived_facts(self):
        engine = Engine([Rule(["A"], "B")])
        engine.add_source("s", "A")
        engine.add_source("t", "A")
        engine.revoke_source("s")
        self.assertEqual(engine.supports("A"), [frozenset({"t"})])
        self.assertEqual(engine.supports("B"), [frozenset({"t"})])

    def test_axiom_rules_fire_with_empty_support(self):
        engine = Engine([Rule([], "TOP"), Rule(["TOP"], "X")])
        self.assertEqual(engine.supports("TOP"), [frozenset()])
        self.assertEqual(engine.supports("X"), [frozenset()])
        # Facts with empty support survive any source revocation.
        engine.revoke_source("anything")
        self.assertTrue(engine.supported("X"))

    def test_trace_is_expanded_and_grounded(self):
        engine = Engine([Rule(["A"], "B"), Rule(["B"], "A")])
        engine.add_source("t", "B")
        trace = engine.trace("A")
        self.assertEqual(trace["fact"], "A")
        self.assertEqual(trace["support"], ["t"])
        self.assertEqual(trace["kind"], "rule")
        child = trace["children"][0]
        self.assertEqual(child["fact"], "B")
        self.assertEqual(child["kind"], "base")
        self.assertEqual(child["source"], "t")


class BudgetTests(unittest.TestCase):
    def test_budget_interruption_flag_is_set(self):
        # 20 independent sources feeding a 2-premise rule creates 400
        # candidate combinations.
        engine = Engine([Rule(["L", "R"], "C")], budget=100)
        for i in range(20):
            engine.add_source(f"l{i}", "L")
            engine.add_source(f"r{i}", "R")
        self.assertTrue(engine.interrupted)
        self.assertGreater(engine.derivations, 100)

    def test_budget_zero_with_no_work_is_clean(self):
        engine = Engine([Rule(["A"], "B")], budget=0)
        self.assertFalse(engine.interrupted)

    def test_budget_zero_interrupts_base_assertion(self):
        engine = Engine([Rule(["A"], "B")], budget=0)
        engine.add_source("s", "A")
        self.assertTrue(engine.interrupted)

    def test_partial_state_is_a_sound_prefix(self):
        engine = Engine([Rule(["L", "R"], "C")], budget=250)
        for i in range(20):
            engine.add_source(f"l{i}", "L")
            engine.add_source(f"r{i}", "R")
        # Reported supports of C must all be genuine unions of real sources.
        for support in engine.supports("C"):
            self.assertEqual(len(support), 2)
            self.assertTrue(all(
                source.startswith(("l", "r")) for source in support
            ))


class ValidationTests(unittest.TestCase):
    def test_empty_tokens_rejected(self):
        with self.assertRaises(ValueError):
            Rule(["A"], "")
        with self.assertRaises(ValueError):
            Rule([""], "B")

    def test_duplicate_premises_deduplicated(self):
        self.assertEqual(Rule(["A", "A"], "B").premises, ("A",))

    def test_bad_budget_rejected(self):
        with self.assertRaises(ValueError):
            Engine(budget=-1)


if __name__ == "__main__":
    unittest.main(verbosity=2)
