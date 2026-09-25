"""Unittest suite for the shared-rule DAG support-set maintenance system."""

import itertools
import json
import unittest

from supportdag import (
    Alt,
    Engine,
    Fact,
    FactRef,
    Rule,
    Support,
    SupportEnumerator,
    fact_lit,
    goal_lit,
    neg_lit,
)
from verifier import Proof, replay_proof, verify_support


def sup(*fact_ids, neg=(), version=1):
    return Support(
        frozenset(FactRef(fid, version) for fid in fact_ids),
        frozenset(neg),
    )


def pos_sets(supports):
    return {frozenset(f.fact_id for f in s.pos) for s in supports}


def drain(enumerator):
    out = []
    while True:
        item = enumerator.next()
        if item is None:
            break
        out.append(item)
    return out, enumerator.result()


def build_engine(facts, rules):
    engine = Engine()
    for fid, cost in facts:
        engine.add_fact(Fact(fid, 1, cost))
    for rule in rules:
        engine.put_rule(rule)
    return engine


class DominanceTest(unittest.TestCase):
    def test_inclusion_dominance_relation(self):
        sa, sab, sc = sup("A"), sup("A", "B"), sup("C")
        self.assertTrue(sa.dominates(sab))     # {A} dominates {A,B}
        self.assertFalse(sab.dominates(sa))
        self.assertFalse(sa.dominates(sc))     # {A} does not dominate {C}
        self.assertFalse(sc.dominates(sa))

    def test_engine_filters_dominated_supports(self):
        engine = build_engine(
            [("A", 1.0), ("B", 1.0), ("C", 1.0)],
            [Rule("RG", "G", 1, (
                Alt("a1", (fact_lit("A"),)),
                Alt("a2", (fact_lit("A"), fact_lit("B"))),
                Alt("a3", (fact_lit("C"),)),
            ))],
        )
        supports = engine.supports_of("G")
        self.assertEqual(pos_sets(supports), {frozenset({"A"}), frozenset({"C"})})


class RetractionTest(unittest.TestCase):
    def test_pruned_alternative_reappears_after_retraction(self):
        rules = [Rule("RH", "H", 1, (
            Alt("cheap", (fact_lit("A"),)),
            Alt("backup", (fact_lit("C"), fact_lit("D"))),
        ))]
        engine = build_engine([("A", 1.0), ("C", 1.0), ("D", 1.0)], rules)

        # Budget of 1: the cheap proof {A} hides the alternative.
        enum = SupportEnumerator(engine, "H", max_results=1)
        got, result = drain(enum)
        self.assertEqual(pos_sets(got), {frozenset({"A"})})
        self.assertFalse(result.complete)
        self.assertEqual(result.reason, "budget")

        # Retract A: the pruned alternative proof must reappear.
        engine.retract_fact("A")
        got2, result2 = drain(SupportEnumerator(engine, "H"))
        self.assertEqual(pos_sets(got2), {frozenset({"C", "D"})})
        self.assertTrue(result2.complete)


class NegationTest(unittest.TestCase):
    def test_negated_premise_invalidated_by_new_fact(self):
        rules = [Rule("RN", "N", 1, (
            Alt("a1", (fact_lit("X"), neg_lit("Y"))),
        ))]
        engine = build_engine([("X", 1.0)], rules)

        supports = engine.supports_of("N")
        self.assertEqual(supports, [sup("X", neg=("Y",))])

        engine.add_fact(Fact("Y", 1))            # negated premise now fails
        self.assertEqual(engine.supports_of("N"), [])

        engine.retract_fact("Y")                 # and recovers on retraction
        self.assertEqual(engine.supports_of("N"), [sup("X", neg=("Y",))])


class SharedSubproofTest(unittest.TestCase):
    def test_shared_subproof_no_cross_contamination(self):
        rules = [
            Rule("RS", "S", 1, (Alt("a1", (fact_lit("C"),)),)),
            Rule("RG1", "G1", 1, (Alt("a1", (goal_lit("S"), fact_lit("A"))),)),
            Rule("RG2", "G2", 1, (Alt("a1", (goal_lit("S"), fact_lit("B"))),)),
        ]
        engine = build_engine([("A", 1.0), ("B", 1.0), ("C", 1.0)], rules)

        g1 = engine.supports_of("G1")
        g2 = engine.supports_of("G2")
        self.assertEqual(pos_sets(g1), {frozenset({"A", "C"})})
        self.assertEqual(pos_sets(g2), {frozenset({"B", "C"})})
        self.assertIn("S", engine._cache)  # subproof shared through the cache


class CycleTest(unittest.TestCase):
    def test_ungrounded_cycle_derives_nothing(self):
        rules = [
            Rule("RP", "P", 1, (Alt("a1", (goal_lit("Q"),)),)),
            Rule("RQ", "Q", 1, (Alt("a1", (goal_lit("P"),)),)),
        ]
        engine = build_engine([], rules)
        self.assertEqual(engine.supports_of("P"), [])
        self.assertEqual(engine.supports_of("Q"), [])

    def test_grounded_cycle_uses_base_support(self):
        rules = [
            Rule("RP", "P", 1, (Alt("a1", (goal_lit("Q"),)),)),
            Rule("RQ", "Q", 1, (
                Alt("loop", (goal_lit("P"),)),
                Alt("base", (fact_lit("Z"),)),
            )),
        ]
        engine = build_engine([("Z", 1.0)], rules)
        self.assertEqual(engine.supports_of("P"), [sup("Z")])
        self.assertEqual(engine.supports_of("Q"), [sup("Z")])


class IncrementalTest(unittest.TestCase):
    def setUp(self):
        rules = [
            Rule("RS", "S", 1, (Alt("a1", (fact_lit("C"),)),)),
            Rule("RG1", "G1", 1, (Alt("a1", (goal_lit("S"), fact_lit("A"))),)),
            Rule("RG2", "G2", 1, (Alt("a1", (goal_lit("S"), fact_lit("B"))),)),
        ]
        self.engine = build_engine(
            [("A", 1.0), ("B", 1.0), ("C", 1.0)], rules)
        self.engine.supports_of("G1")
        self.engine.supports_of("G2")

    def test_rule_version_change_revises_only_dependents(self):
        entry_g2 = self.engine._cache["G2"]
        self.engine.put_rule(Rule("RG1", "G1", 2, (
            Alt("a1", (goal_lit("S"), fact_lit("A"), neg_lit("E"))),
        )))
        # Unrelated goal keeps its cached entry untouched.
        self.assertIs(self.engine._cache["G2"], entry_g2)
        # Dependent goal is revised to the new rule version.
        self.assertEqual(self.engine.supports_of("G1"),
                         [sup("A", "C", neg=("E",))])

    def test_fact_version_bump_revises_dependents(self):
        before = self.engine.supports_of("G1")
        self.assertEqual(before, [sup("A", "C")])
        self.engine.add_fact(Fact("A", 2, 1.0))
        after = self.engine.supports_of("G1")
        expected = Support(
            frozenset({FactRef("A", 2), FactRef("C", 1)}), frozenset())
        self.assertEqual(after, [expected])
        self.assertNotEqual(before, after)
        # The stale support no longer verifies.
        report = verify_support(self.engine, "G1", before[0])
        self.assertFalse(report.ok)
        self.assertTrue(report.stale)


class ExhaustiveSmallGraphTest(unittest.TestCase):
    """Brute-force comparison of every minimal support on a small graph."""

    RULES = [
        Rule("R1", "G1", 1, (
            Alt("a1", (fact_lit("A"), fact_lit("B"))),
            Alt("a2", (fact_lit("C"), neg_lit("E"))),
        )),
        Rule("R2", "G2", 1, (
            Alt("a1", (goal_lit("G1"), fact_lit("D"))),
            Alt("a2", (fact_lit("A"), fact_lit("D"))),
        )),
    ]

    def setUp(self):
        self.engine = build_engine(
            [("A", 1.0), ("B", 1.0), ("C", 1.0), ("D", 1.0)], self.RULES)

    def _derivable(self, allowed, goal, visiting):
        for rid in self.engine.rule_ids_for(goal):
            rule = self.engine.rules[rid]
            for alt in rule.alts:
                if self._alt_ok(alt, allowed, visiting):
                    return True
        return False

    def _alt_ok(self, alt, allowed, visiting):
        for lit in alt.lits:
            if lit.kind == "fact":
                if lit.ref not in allowed:
                    return False
            elif lit.kind == "negfact":
                if lit.ref in self.engine.facts:
                    return False
            else:
                if lit.ref in visiting:
                    return False
                if not self._derivable(allowed, lit.ref, visiting | {lit.ref}):
                    return False
        return True

    def test_exhaustive_minimal_sets_match(self):
        present = sorted(self.engine.facts)
        expected = []
        for size in range(len(present) + 1):
            for combo in itertools.combinations(present, size):
                candidate = set(combo)
                if any(prev <= candidate for prev in expected):
                    continue
                if self._derivable(candidate, "G2", frozenset({"G2"})):
                    expected.append(candidate)
        expected = {frozenset(s) for s in expected}
        self.assertEqual(expected, {frozenset({"A", "D"}), frozenset({"C", "D"})})

        supports = self.engine.supports_of("G2")
        self.assertEqual(pos_sets(supports), expected)
        by_pos = {frozenset(f.fact_id for f in s.pos): s for s in supports}
        self.assertEqual(by_pos[frozenset({"C", "D"})].neg, frozenset({"E"}))
        self.assertEqual(by_pos[frozenset({"A", "D"})].neg, frozenset())


class InclusionVsCardinalityTest(unittest.TestCase):
    def test_inclusion_minimal_is_not_minimum_cardinality(self):
        rules = [Rule("RM", "M", 1, (
            Alt("one", (fact_lit("A"),)),
            Alt("two", (fact_lit("C"), fact_lit("D"))),
        ))]
        engine = build_engine([("A", 1.0), ("C", 1.0), ("D", 1.0)], rules)
        supports = engine.supports_of("M")
        # Both are inclusion-minimal even though cardinalities differ.
        self.assertEqual(pos_sets(supports),
                         {frozenset({"A"}), frozenset({"C", "D"})})
        self.assertEqual(sorted(len(s.pos) for s in supports), [1, 2])


class BudgetTest(unittest.TestCase):
    def setUp(self):
        rules = [Rule("RM", "M", 1, (
            Alt("a", (fact_lit("A"),)),
            Alt("b", (fact_lit("B"),)),
            Alt("c", (fact_lit("C"),)),
        ))]
        self.engine = build_engine(
            [("A", 3.0), ("B", 1.0), ("C", 2.0)], rules)

    def test_cost_ordered_lazy_enumeration(self):
        got, result = drain(SupportEnumerator(self.engine, "M"))
        ordered = [[f.fact_id for f in s.pos] for s in got]
        self.assertEqual(ordered, [["B"], ["C"], ["A"]])
        self.assertTrue(result.complete)

    def test_result_budget_reports_incomplete(self):
        enum = SupportEnumerator(self.engine, "M", max_results=2)
        got, result = drain(enum)
        self.assertEqual(len(got), 2)
        self.assertFalse(result.complete)
        self.assertEqual(result.reason, "budget")

    def test_cost_budget_reports_incomplete(self):
        enum = SupportEnumerator(self.engine, "M", max_cost=1.5)
        got, result = drain(enum)
        self.assertEqual(pos_sets(got), {frozenset({"B"})})
        self.assertFalse(result.complete)
        self.assertEqual(result.reason, "budget")


class PauseResumeTest(unittest.TestCase):
    def setUp(self):
        rules = [Rule("RM", "M", 1, (
            Alt("a", (fact_lit("A"),)),
            Alt("b", (fact_lit("B"),)),
            Alt("c", (fact_lit("C"),)),
        ))]
        self.engine = build_engine(
            [("A", 3.0), ("B", 1.0), ("C", 2.0)], rules)

    def test_resume_matches_uninterrupted_run(self):
        full, _ = drain(SupportEnumerator(self.engine, "M"))

        enum1 = SupportEnumerator(self.engine, "M")
        first = enum1.next()
        state1 = json.loads(json.dumps(enum1.checkpoint()))  # JSON round-trip

        enum2 = SupportEnumerator.resume(self.engine, state1)
        second = enum2.next()
        enum3 = SupportEnumerator.resume(self.engine, enum2.checkpoint())
        third = enum3.next()
        self.assertIsNone(enum3.next())
        self.assertEqual([first, second, third], full)
        self.assertTrue(enum3.result().complete)

    def test_checkpoint_before_start(self):
        enum = SupportEnumerator(self.engine, "M")
        state = enum.checkpoint()
        resumed = SupportEnumerator.resume(self.engine, state)
        got, result = drain(resumed)
        self.assertEqual(pos_sets(got),
                         {frozenset({"A"}), frozenset({"B"}), frozenset({"C"})})
        self.assertTrue(result.complete)

    def test_resume_rejects_stale_engine_state(self):
        enum = SupportEnumerator(self.engine, "M")
        enum.next()
        state = enum.checkpoint()
        self.engine.add_fact(Fact("A", 2, 3.0))  # version drift
        with self.assertRaises(ValueError):
            SupportEnumerator.resume(self.engine, state)


class VerifierTest(unittest.TestCase):
    def setUp(self):
        self.engine = build_engine(
            [("A", 1.0), ("B", 1.0), ("C", 1.0), ("D", 1.0)],
            ExhaustiveSmallGraphTest.RULES)
        self.supports = self.engine.supports_of("G2")

    def test_every_engine_support_verifies(self):
        for support in self.supports:
            report = verify_support(self.engine, "G2", support)
            self.assertTrue(report.ok, report.reasons)
            self.assertTrue(report.sufficient)
            self.assertTrue(report.minimal)
            self.assertIsNotNone(report.proof)

    def test_insufficient_support_rejected(self):
        report = verify_support(self.engine, "G2", sup("A"))
        self.assertFalse(report.ok)
        self.assertFalse(report.sufficient)

    def test_redundant_fact_rejected(self):
        report = verify_support(self.engine, "G2", sup("A", "C", "D"))
        self.assertFalse(report.ok)
        self.assertTrue(report.sufficient)
        self.assertFalse(report.minimal)

    def test_missing_negation_record_rejected(self):
        # {C,D} relies on the absence of E; omitting it must fail.
        report = verify_support(self.engine, "G2", sup("C", "D"))
        self.assertFalse(report.ok)
        self.assertFalse(report.minimal)

    def test_proof_replay_and_staleness_detection(self):
        support = next(s for s in self.supports
                       if {f.fact_id for f in s.pos} == {"A", "D"})
        report = verify_support(self.engine, "G2", support)
        self.assertTrue(replay_proof(self.engine, report.proof))

        # Proof survives JSON serialization (evidence is replayable).
        clone = Proof.from_json(json.loads(json.dumps(report.proof.to_json())))
        self.assertTrue(replay_proof(self.engine, clone))

        # Any version drift invalidates the replay.
        self.engine.add_fact(Fact("D", 2, 1.0))
        self.assertFalse(replay_proof(self.engine, report.proof))

    def test_proof_step_ids_are_stable(self):
        support = self.supports[0]
        first = verify_support(self.engine, "G2", support)
        second = verify_support(self.engine, "G2", support)
        self.assertEqual(first.proof.root, second.proof.root)
        self.assertEqual(set(first.proof.steps), set(second.proof.steps))
        for step in first.proof.steps.values():
            self.assertTrue(step.step_id)
            self.assertIsNotNone(step.version)


if __name__ == "__main__":
    unittest.main()
