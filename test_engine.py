import unittest

from engine import (
    Condition,
    Derivation,
    Engine,
    Fact,
    InterventionError,
    Remove,
    Replace,
    ReplayError,
    replay,
    Rule,
    Step,
    apply_ops,
    derive,
)

GOAL = "approval.status"


def make_engine() -> Engine:
    facts = (
        Fact(id="F1", assertion="applicant.role", value="employee", version=1),
        Fact(id="F2", assertion="expense.amount_band", value="low", version=1),
        Fact(id="F3", assertion="expense.has_receipt", value=True, version=1),
    )
    rules = (
        Rule(
            id="R1",
            version=2,
            conditions=(
                Condition("applicant.role", "employee"),
                Condition("expense.amount_band", "low"),
            ),
            conclusion=Condition("expense.precheck", "pass"),
        ),
        Rule(
            id="R2",
            version=1,
            conditions=(
                Condition("expense.precheck", "pass"),
                Condition("expense.has_receipt", True),
            ),
            conclusion=Condition(GOAL, "approved"),
        ),
    )
    return Engine(facts, rules, goal_assertion=GOAL)


class TestBaseline(unittest.TestCase):
    def test_goal_reached_with_stable_ids_and_versions(self):
        d = make_engine().baseline()
        self.assertEqual(d.goal(GOAL).value, "approved")
        step_rules = [(s.rule_id, s.rule_version) for s in d.steps]
        self.assertEqual(step_rules, [("R1", 2), ("R2", 1)])
        self.assertEqual([s.id for s in d.steps], ["S0001", "S0002"])
        self.assertEqual(d.steps[0].inputs, (("F1", 1), ("F2", 1)))

    def test_support_chain_contains_exactly_goal_steps(self):
        d = make_engine().baseline()
        chain = d.support_chain(GOAL)
        self.assertEqual([s.id for s in chain], ["S0001", "S0002"])

    def test_summary_is_deterministic(self):
        engine = make_engine()
        self.assertEqual(engine.baseline().summary(), engine.baseline().summary())


class TestInterventions(unittest.TestCase):
    def test_remove_fact_loses_goal_and_baseline_intact(self):
        engine = make_engine()
        result = engine.intervene([Remove("F3")])
        self.assertIsNone(result.derivation.goal(GOAL))
        self.assertTrue(result.baseline_intact)
        removed_rules = [s.rule_id for s in result.removed_steps]
        self.assertEqual(removed_rules, ["R2"])

    def test_replace_fact_bumps_version_and_drops_old_value(self):
        engine = make_engine()
        result = engine.intervene([Replace("F2", "high")])
        replaced = result.derivation.facts["F2"]
        self.assertEqual(replaced.value, "high")
        self.assertEqual(replaced.version, 2)
        values = [f.value for f in result.derivation.facts.values() if f.id == "F2"]
        self.assertEqual(values, ["high"])  # 旧值不同时保留
        self.assertIsNone(result.derivation.goal(GOAL))
        self.assertTrue(result.baseline_intact)

    def test_contradictory_remove_and_replace_rejected(self):
        engine = make_engine()
        with self.assertRaises(InterventionError):
            engine.intervene([Remove("F2"), Replace("F2", "high")])
        with self.assertRaises(InterventionError):
            engine.intervene([Replace("F2", "high"), Remove("F2")])

    def test_contradictory_double_replace_rejected(self):
        engine = make_engine()
        with self.assertRaises(InterventionError):
            engine.intervene([Replace("F2", "high"), Replace("F2", "mid")])

    def test_duplicate_identical_ops_are_idempotent(self):
        engine = make_engine()
        result = engine.intervene([Replace("F2", "high"), Replace("F2", "high"), Remove("F3"), Remove("F3")])
        self.assertIsNone(result.derivation.goal(GOAL))
        self.assertTrue(result.baseline_intact)

    def test_unknown_fact_rejected(self):
        engine = make_engine()
        with self.assertRaises(InterventionError):
            engine.intervene([Remove("F404")])
        with self.assertRaises(InterventionError):
            engine.intervene([Replace("F404", "x")])

    def test_atomicity_no_partial_application(self):
        engine = make_engine()
        try:
            engine.intervene([Replace("F2", "high"), Remove("F404")])
        except InterventionError:
            pass
        # 失败场景不留下任何痕迹: 基线仍完整可推导
        self.assertEqual(engine.baseline().goal(GOAL).value, "approved")

    def test_scenarios_are_isolated(self):
        engine = make_engine()
        engine.intervene([Replace("F2", "high")])
        second = engine.intervene([Remove("F3")])
        # 第二个场景不受第一个影响: F2 仍是基线值 low
        self.assertEqual(second.derivation.facts["F2"].value, "low")
        self.assertEqual(second.derivation.facts["F2"].version, 1)

    def test_baseline_summary_unchanged_after_all_interventions(self):
        engine = make_engine()
        before = engine.baseline().summary()
        engine.intervene([Remove("F3")])
        engine.intervene([Replace("F2", "high")])
        engine.intervene([Replace("F2", "high"), Replace("F3", False)])
        self.assertEqual(engine.baseline().summary(), before)


class TestReplay(unittest.TestCase):
    def test_replay_matches_recorded_evidence(self):
        engine = make_engine()
        d = engine.baseline()
        replayed = replay(engine.base_facts, engine.rules, d.steps)
        self.assertEqual(replayed.summary(), d.summary())

    def test_replay_scenario_evidence(self):
        engine = make_engine()
        result = engine.intervene([Remove("F3")])
        new_facts = apply_ops(engine.base_facts, [Remove("F3")])
        replayed = replay(new_facts, engine.rules, result.derivation.steps)
        self.assertEqual(replayed.summary(), result.derivation.summary())

    def test_tampered_step_detected(self):
        engine = make_engine()
        d = engine.baseline()
        bad = Step(
            id="S0001",
            rule_id="R1",
            rule_version=99,  # 篡改规则版本
            inputs=d.steps[0].inputs,
            output=d.steps[0].output,
        )
        with self.assertRaises(ReplayError):
            replay(engine.base_facts, engine.rules, (bad,) + d.steps[1:])

    def test_truncated_evidence_detected(self):
        engine = make_engine()
        d = engine.baseline()
        with self.assertRaises(ReplayError):
            replay(engine.base_facts, engine.rules, d.steps[:1])


class TestDeterminism(unittest.TestCase):
    def test_derivation_order_independent_of_input_order(self):
        engine = make_engine()
        shuffled_facts = tuple(reversed(engine.base_facts))
        shuffled_rules = tuple(reversed(engine.rules))
        d1 = engine.baseline()
        d2 = derive(shuffled_facts, shuffled_rules)
        self.assertEqual(d1.summary(), d2.summary())


if __name__ == "__main__":
    unittest.main(verbosity=2)
