import unittest

from counterfactual import (
    Engine,
    Fact,
    Intervention,
    InterventionError,
    RemoveFact,
    ReplaceFact,
    Rule,
)

FACTS = (
    Fact("F-AGE", "age", "adult", 1),
    Fact("F-INCOME", "income", "high", 1),
    Fact("F-DEBT", "debt", "low", 1),
    Fact("F-REGION", "region", "supported", 1),
)
RULES = (
    Rule("R-CREDIT", (("age", "adult"), ("income", "high")), ("credit", "good"), 1),
    Rule("R-RISK", (("credit", "good"), ("debt", "low")), ("risk", "low"), 1),
    Rule("R-APPROVE", (("risk", "low"), ("region", "supported")), ("loan", "approved"), 1),
)


def make_engine() -> Engine:
    return Engine(FACTS, RULES)


class BaselineTests(unittest.TestCase):
    def test_goal_holds_in_baseline(self):
        self.assertTrue(make_engine().baseline().holds("loan", "approved"))

    def test_stable_ids_and_versions(self):
        engine = make_engine()
        for fact in engine.facts:
            self.assertTrue(fact.fact_id.startswith("F-"))
            self.assertEqual(fact.version, 1)
        for rule in engine.rules:
            self.assertTrue(rule.rule_id.startswith("R-"))
            self.assertEqual(rule.version, 1)
        for step in engine.baseline().steps:
            self.assertTrue(step.step_id.startswith("S-"))
            self.assertEqual(step.version, 1)

    def test_derivation_is_deterministic(self):
        self.assertEqual(make_engine().baseline_summary(), make_engine().baseline_summary())

    def test_support_chain_traces_goal_to_sources(self):
        chain = make_engine().baseline().support_chain("loan")
        self.assertEqual([s.rule_id for s in chain["steps"]],
                         ["R-CREDIT", "R-RISK", "R-APPROVE"])
        self.assertEqual({f.fact_id for f in chain["source_facts"]},
                         {"F-AGE", "F-INCOME", "F-DEBT", "F-REGION"})

    def test_evidence_is_replayable(self):
        self.assertTrue(make_engine().baseline().replay(RULES))


class InterventionTests(unittest.TestCase):
    def test_remove_fact_breaks_goal_and_restores_baseline(self):
        engine = make_engine()
        before = engine.baseline_summary()
        with engine.intervene(RemoveFact("F-INCOME")) as session:
            self.assertFalse(session.derivation.holds("loan", "approved"))
            self.assertNotIn("income", session.derivation.facts)
        self.assertEqual(engine.baseline_summary(), before)
        self.assertTrue(engine.baseline().holds("loan", "approved"))

    def test_replace_fact_drops_old_value(self):
        engine = make_engine()
        with engine.intervene(ReplaceFact("F-DEBT", "high")) as session:
            debt = session.derivation.facts["debt"]
            self.assertEqual(debt.value, "high")
            self.assertEqual(debt.fact_id, "F-DEBT")   # same stable ID
            self.assertEqual(debt.version, 2)          # bumped version
            values = [f.value for f in session.derivation.facts.values()
                      if f.fact_id == "F-DEBT"]
            self.assertNotIn("low", values)            # old value not retained
            self.assertFalse(session.derivation.holds("loan", "approved"))

    def test_replace_with_same_value_rejected(self):
        with self.assertRaises(InterventionError):
            Intervention((ReplaceFact("F-DEBT", "low"),)).apply(FACTS)

    def test_multiple_ops_are_atomic_and_isolated(self):
        engine = make_engine()
        with engine.intervene(RemoveFact("F-REGION"),
                              ReplaceFact("F-INCOME", "low")) as first:
            self.assertNotIn("region", first.derivation.facts)
            self.assertEqual(first.derivation.facts["income"].value, "low")
        with engine.intervene(RemoveFact("F-REGION")) as second:
            # isolation: the earlier replace does not leak into this session
            self.assertEqual(second.derivation.facts["income"].value, "high")
        self.assertTrue(engine.baseline().holds("loan", "approved"))

    def test_failed_apply_leaves_facts_untouched(self):
        intervention = Intervention((RemoveFact("F-AGE"), RemoveFact("F-NOPE")))
        with self.assertRaises(InterventionError):
            intervention.apply(FACTS)
        self.assertEqual(len(FACTS), 4)  # nothing applied partially

    def test_unknown_fact_rejected(self):
        with self.assertRaises(InterventionError):
            Intervention((RemoveFact("F-NOPE"),)).apply(FACTS)


class ContradictionTests(unittest.TestCase):
    def test_conflicting_replaces_rejected_upfront(self):
        with self.assertRaises(InterventionError):
            Intervention((ReplaceFact("F-DEBT", "high"),
                          ReplaceFact("F-DEBT", "medium")))

    def test_remove_and_replace_same_fact_rejected_upfront(self):
        with self.assertRaises(InterventionError):
            Intervention((RemoveFact("F-DEBT"), ReplaceFact("F-DEBT", "high")))
        with self.assertRaises(InterventionError):
            Intervention((ReplaceFact("F-DEBT", "high"), RemoveFact("F-DEBT")))

    def test_double_remove_rejected_upfront(self):
        with self.assertRaises(InterventionError):
            Intervention((RemoveFact("F-DEBT"), RemoveFact("F-DEBT")))

    def test_contradiction_rejected_before_session_opens(self):
        engine = make_engine()
        before = engine.baseline_summary()
        with self.assertRaises(InterventionError):
            with engine.intervene(RemoveFact("F-DEBT"), RemoveFact("F-DEBT")):
                pass
        self.assertEqual(engine.baseline_summary(), before)


if __name__ == "__main__":
    unittest.main()
