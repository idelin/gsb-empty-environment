import unittest

from tms import Engine, Fact, Rule
from tms.core import ASSUMPTION


def base_engine():
    engine = Engine()
    engine.add_rule(Rule("r1", 1, {"A", "B"}, "C"))
    engine.add_rule(Rule("r2", 1, {"D"}, "C"))
    return engine


class ConjunctivePremisesTest(unittest.TestCase):
    def test_single_premise_does_not_fire(self):
        engine = base_engine()
        engine.assert_fact(Fact("fA", 1, "A"))
        self.assertFalse(engine.is_in("C"))

    def test_all_premises_fire(self):
        engine = base_engine()
        engine.assert_fact(Fact("fA", 1, "A"))
        engine.assert_fact(Fact("fB", 1, "B"))
        self.assertTrue(engine.is_in("C"))
        self.assertEqual(engine.support_of("C"), frozenset({"r1"}))


class AlternativeRulesTest(unittest.TestCase):
    def test_retract_one_of_two_supports_keeps_conclusion(self):
        engine = base_engine()
        for fact in (Fact("fA", 1, "A"), Fact("fB", 1, "B"), Fact("fD", 1, "D")):
            engine.assert_fact(fact)
        self.assertEqual(engine.support_of("C"), frozenset({"r1", "r2"}))

        engine.retract_fact("fB")
        self.assertTrue(engine.is_in("C"))
        # minimal support: stale r1 entry is gone, only r2 remains
        self.assertEqual(engine.support_of("C"), frozenset({"r2"}))

        engine.retract_fact("fD")
        self.assertFalse(engine.is_in("C"))
        self.assertEqual(engine.support_of("C"), frozenset())

    def test_support_set_is_minimal_after_each_step(self):
        engine = base_engine()
        engine.assert_fact(Fact("fA", 1, "A"))
        engine.assert_fact(Fact("fB", 1, "B"))
        engine.assert_fact(Fact("fD", 1, "D"))
        engine.retract_fact("fD")
        self.assertEqual(engine.support_of("C"), frozenset({"r1"}))
        engine.retract_fact("fA")
        self.assertFalse(engine.is_in("C"))


class DuplicateFactTest(unittest.TestCase):
    def test_duplicate_assert_is_noop_and_keeps_counts(self):
        engine = base_engine()
        engine.assert_fact(Fact("fA", 1, "A"))
        engine.assert_fact(Fact("fB", 1, "B"))
        before = engine.support_count("C")
        step = engine.assert_fact(Fact("fA", 1, "A"))
        self.assertTrue(step.noop)
        self.assertEqual(engine.support_count("C"), before)
        self.assertEqual(engine.support_count("A"), 1)

    def test_two_facts_for_same_proposition_count_once(self):
        engine = base_engine()
        engine.assert_fact(Fact("fA1", 1, "A"))
        engine.assert_fact(Fact("fA2", 1, "A"))
        self.assertEqual(engine.support_of("A"), frozenset({ASSUMPTION}))
        self.assertEqual(engine.support_count("A"), 1)


class CycleTest(unittest.TestCase):
    def make_cycle(self):
        engine = Engine()
        engine.add_rule(Rule("r3", 1, {"X"}, "Y"))
        engine.add_rule(Rule("r4", 1, {"Y"}, "X"))
        return engine

    def test_groundless_cycle_stays_out(self):
        engine = self.make_cycle()
        self.assertFalse(engine.is_in("X"))
        self.assertFalse(engine.is_in("Y"))

    def test_grounded_cycle_is_in_then_collapses(self):
        engine = self.make_cycle()
        engine.assert_fact(Fact("fX", 1, "X"))
        self.assertTrue(engine.is_in("X"))
        self.assertTrue(engine.is_in("Y"))
        engine.retract_fact("fX")
        self.assertFalse(engine.is_in("X"))
        self.assertFalse(engine.is_in("Y"))


class IdentityTest(unittest.TestCase):
    def test_conflicting_rule_id_rejected(self):
        engine = Engine()
        engine.add_rule(Rule("r1", 1, {"A"}, "C"))
        with self.assertRaises(ValueError):
            engine.add_rule(Rule("r1", 2, {"A"}, "C"))

    def test_conflicting_fact_id_rejected(self):
        engine = Engine()
        engine.assert_fact(Fact("fA", 1, "A"))
        with self.assertRaises(ValueError):
            engine.assert_fact(Fact("fA", 2, "A"))

    def test_steps_have_stable_ids_and_versions(self):
        engine = base_engine()
        engine.assert_fact(Fact("fA", 1, "A"))
        engine.retract_fact("fA")
        ids = [s.step_id for s in engine.steps]
        self.assertEqual(ids, ["step-%04d" % i for i in range(1, len(ids) + 1)])
        self.assertTrue(all(s.version == 1 for s in engine.steps))

    def test_retract_unknown_fact_is_noop(self):
        engine = Engine()
        step = engine.retract_fact("nope")
        self.assertTrue(step.noop)


class ReplayTest(unittest.TestCase):
    def build_history(self):
        engine = base_engine()
        engine.assert_fact(Fact("fA", 1, "A"))
        engine.assert_fact(Fact("fB", 1, "B"))
        engine.assert_fact(Fact("fD", 1, "D"))
        engine.assert_fact(Fact("fA", 1, "A"))  # duplicate
        engine.retract_fact("fB")
        engine.retract_fact("fD")
        return engine

    def test_replay_reproduces_every_state(self):
        engine = self.build_history()
        report = engine.replay()
        self.assertTrue(report.ok)
        self.assertEqual(report.steps_checked, len(engine.steps))

    def test_tampered_log_is_detected(self):
        engine = self.build_history()
        victim = engine.steps[2]
        engine.steps[2] = type(victim)(
            victim.step_id, victim.version, victim.op, victim.args,
            victim.noop, "0" * 64,
        )
        report = engine.replay()
        self.assertFalse(report.ok)
        self.assertEqual(report.failed_step_id, victim.step_id)


if __name__ == "__main__":
    unittest.main()
