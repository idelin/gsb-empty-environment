"""Unit tests for the evidence-driven entity resolution engine."""
from __future__ import annotations

import unittest

from entity_resolution import (
    ConflictError,
    DuplicateFactError,
    Engine,
    Fact,
    FactKind,
    FactNotActiveError,
    StepAction,
    UnknownFactError,
)
from entity_resolution import sample
from entity_resolution.rules import ALL_RULES


def equiv(fact_id: str, left: str, right: str, version: int = 1) -> Fact:
    return Fact(fact_id, version, FactKind.EQUIV, left, right, evidence="synthetic")


def nequiv(fact_id: str, left: str, right: str, version: int = 1) -> Fact:
    return Fact(fact_id, version, FactKind.NEQUIV, left, right, evidence="synthetic")


class MergeTests(unittest.TestCase):
    def test_equiv_facts_merge_transitively(self) -> None:
        engine = Engine()
        engine.assert_fact(equiv("f1", "a", "b"))
        engine.assert_fact(equiv("f2", "b", "c"))
        self.assertEqual(engine.component_of("c"), frozenset({"a", "b", "c"}))

    def test_representative_is_deterministic_min_key(self) -> None:
        engine = Engine()
        engine.assert_fact(equiv("f1", "zeta", "alpha"))
        engine.assert_fact(equiv("f2", "alpha", "mid"))
        for key in ("zeta", "alpha", "mid"):
            self.assertEqual(engine.representative(key), "alpha")

    def test_representative_stable_regardless_of_assertion_order(self) -> None:
        first, second = Engine(), Engine()
        first.assert_fact(equiv("f1", "b", "a"))
        first.assert_fact(equiv("f2", "c", "b"))
        second.assert_fact(equiv("f2", "c", "b"))
        second.assert_fact(equiv("f1", "b", "a"))
        self.assertEqual(first.components(), second.components())
        self.assertEqual(first.representative("c"), "a")

    def test_same_name_does_not_merge(self) -> None:
        engine = Engine()
        engine.register_entity("e1", "Alice Zhang")
        engine.register_entity("e4", "Alice Zhang")
        self.assertNotEqual(engine.representative("e1"), engine.representative("e4"))
        self.assertEqual(engine.component_of("e1"), frozenset({"e1"}))
        self.assertEqual(engine.component_of("e4"), frozenset({"e4"}))


class ConflictTests(unittest.TestCase):
    def test_equiv_chain_plus_nequiv_conflicts_with_ring(self) -> None:
        engine = Engine()
        engine.assert_fact(equiv("f1", "a", "b"))
        engine.assert_fact(equiv("f2", "b", "c"))
        with self.assertRaises(ConflictError) as ctx:
            engine.assert_fact(nequiv("f3", "a", "c"))
        ring = ctx.exception.ring
        self.assertEqual(ring.equiv_path_fact_ids, ("f1", "f2"))
        self.assertEqual(ring.nequiv_fact_id, "f3")
        self.assertEqual(ring.fact_ids, ("f1", "f2", "f3"))

    def test_rejected_fact_is_not_recorded(self) -> None:
        engine = Engine()
        engine.assert_fact(equiv("f1", "a", "b"))
        with self.assertRaises(ConflictError):
            engine.assert_fact(nequiv("f2", "a", "b"))
        self.assertNotIn("f2", engine.active_facts())
        self.assertEqual(len(engine.steps), 1)

    def test_equiv_assertion_against_active_nequiv_conflicts(self) -> None:
        engine = Engine()
        engine.assert_fact(nequiv("f1", "a", "b"))
        with self.assertRaises(ConflictError) as ctx:
            engine.assert_fact(equiv("f2", "a", "b"))
        self.assertEqual(ctx.exception.ring.fact_ids, ("f2", "f1"))

    def test_self_nequiv_conflicts_with_degenerate_ring(self) -> None:
        engine = Engine()
        with self.assertRaises(ConflictError) as ctx:
            engine.assert_fact(nequiv("f1", "a", "a"))
        self.assertEqual(ctx.exception.ring.equiv_path_fact_ids, ())
        self.assertEqual(ctx.exception.ring.nequiv_fact_id, "f1")


class RetractionTests(unittest.TestCase):
    def test_retraction_splits_component(self) -> None:
        engine = Engine()
        engine.assert_fact(equiv("f1", "a", "b"))
        engine.assert_fact(equiv("f2", "b", "c"))
        engine.retract_fact("f2")
        self.assertEqual(engine.component_of("a"), frozenset({"a", "b"}))
        self.assertEqual(engine.component_of("c"), frozenset({"c"}))
        self.assertFalse(engine.is_active("f2"))

    def test_retraction_allows_previously_conflicting_nequiv(self) -> None:
        engine = sample.build_engine()
        self.assertEqual(engine.component_of("e1"), frozenset({"e1", "e2"}))
        self.assertEqual(engine.component_of("e3"), frozenset({"e3"}))
        self.assertTrue(engine.is_active("f3"))

    def test_retract_unknown_fact_raises(self) -> None:
        engine = Engine()
        with self.assertRaises(UnknownFactError):
            engine.retract_fact("nope")

    def test_double_retraction_raises(self) -> None:
        engine = Engine()
        engine.assert_fact(equiv("f1", "a", "b"))
        engine.retract_fact("f1")
        with self.assertRaises(FactNotActiveError):
            engine.retract_fact("f1")


class IdentityAndVersionTests(unittest.TestCase):
    def test_duplicate_fact_id_rejected(self) -> None:
        engine = Engine()
        engine.assert_fact(equiv("f1", "a", "b"))
        with self.assertRaises(DuplicateFactError):
            engine.assert_fact(equiv("f1", "c", "d"))

    def test_steps_have_stable_ids_and_versions(self) -> None:
        engine = sample.build_engine()
        steps = engine.steps
        self.assertEqual([s.step_id for s in steps], [f"step-{i:04d}" for i in range(1, 10)])
        self.assertTrue(all(s.version >= 1 for s in steps))
        self.assertEqual([s.seq for s in steps], list(range(1, 10)))
        self.assertTrue(all(s.action is StepAction.REGISTER_ENTITY for s in steps[:5]))
        self.assertEqual(steps[7].action, StepAction.RETRACT_FACT)
        self.assertEqual(steps[7].retract_fact_id, "f2")
        self.assertEqual(steps[8].action, StepAction.ASSERT_FACT)
        self.assertEqual(steps[8].fact, sample.F3)

    def test_facts_rules_steps_all_carry_ids_and_versions(self) -> None:
        engine = sample.build_engine()
        for fact in engine.active_facts().values():
            self.assertTrue(fact.fact_id)
            self.assertGreaterEqual(fact.version, 1)
        for rule in ALL_RULES:
            self.assertTrue(rule.rule_id)
            self.assertGreaterEqual(rule.version, 1)
        for step in engine.steps:
            self.assertTrue(step.step_id)
            self.assertGreaterEqual(step.version, 1)
            if step.action is StepAction.ASSERT_FACT:
                self.assertTrue(step.rule_ids)


class ReplayTests(unittest.TestCase):
    def test_replay_reproduces_components_and_facts(self) -> None:
        engine = sample.build_engine()
        clone = Engine.replay(engine.steps)
        self.assertEqual(clone.components(), engine.components())
        self.assertEqual(clone.active_facts(), engine.active_facts())
        self.assertEqual(clone.steps, engine.steps)

    def test_replay_is_deterministic_across_runs(self) -> None:
        engine = sample.build_engine()
        self.assertEqual(engine.replayed().components(), engine.replayed().components())


if __name__ == "__main__":
    unittest.main()
