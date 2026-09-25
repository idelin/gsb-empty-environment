import unittest

from proofsched import (
    BudgetExhausted,
    Fact,
    Goal,
    GoalStatus,
    GreedyScheduler,
    ProofEngine,
    Rule,
    replay_certificate,
)
from proofsched.sample import build_sample


def tiny_world():
    """a -> b -> c and b -> d; goals on c and d share the a->b subproof."""
    facts = {"a": Fact("a")}
    rules = {
        "r1": Rule("r1", ("a",), "b"),
        "r2": Rule("r2", ("b",), "c"),
        "r3": Rule("r3", ("b",), "d"),
    }
    goals = {
        "g1": Goal("g1", ("c",), 1.0),
        "g2": Goal("g2", ("d",), 1.0),
    }
    return facts, rules, goals


class BudgetTests(unittest.TestCase):
    def test_budget_zero_all_undecided(self):
        facts, rules, goals = build_sample()
        engine = ProofEngine(facts, rules, goals, budget=0)
        report = engine.status_report()
        self.assertTrue(report)
        self.assertTrue(all(s is GoalStatus.UNDECIDED for s in report.values()))

    def test_budget_zero_blocks_steps(self):
        facts, rules, goals = tiny_world()
        engine = ProofEngine(facts, rules, goals, budget=0)
        with self.assertRaises(BudgetExhausted):
            engine.apply("r1")

    def test_negative_budget_rejected(self):
        facts, rules, goals = tiny_world()
        with self.assertRaises(ValueError):
            ProofEngine(facts, rules, goals, budget=-1)

    def test_add_budget_never_revokes_proved(self):
        facts, rules, goals = build_sample()
        engine = ProofEngine(facts, rules, goals, budget=4)
        GreedyScheduler(engine).run()
        before = engine.status_report()
        self.assertTrue(any(s is GoalStatus.PROVED for s in before.values()))
        engine.add_budget(3)
        GreedyScheduler(engine).run(exhaust=True)
        after = engine.status_report()
        for gid, status in before.items():
            if status is GoalStatus.PROVED:
                self.assertIs(after[gid], GoalStatus.PROVED, gid)

    def test_proved_weight_monotone_in_budget(self):
        facts, rules, goals = build_sample()
        weights = []
        for budget in range(0, 8):
            engine = ProofEngine(facts, rules, goals, budget=budget)
            GreedyScheduler(engine).run(exhaust=True)
            weights.append(engine.proved_weight())
        self.assertEqual(weights, sorted(weights))


class SharingTests(unittest.TestCase):
    def test_shared_subproof_computed_once(self):
        facts, rules, goals = tiny_world()
        engine = ProofEngine(facts, rules, goals, budget=3)
        GreedyScheduler(engine).run()
        self.assertIs(engine.goal_status("g1"), GoalStatus.PROVED)
        self.assertIs(engine.goal_status("g2"), GoalStatus.PROVED)
        # 3 unique steps for 2 goals whose proof trees total 4 rule nodes.
        self.assertEqual(engine.stats.steps_computed, 3)
        self.assertEqual(engine.shared_work_avoided(), 1)

    def test_reapplying_valid_rule_is_free(self):
        facts, rules, goals = tiny_world()
        engine = ProofEngine(facts, rules, goals, budget=1)
        self.assertTrue(engine.apply("r1"))
        self.assertFalse(engine.apply("r1"))  # cache hit, no new computation
        self.assertEqual(engine.budget, 0)
        self.assertEqual(engine.stats.steps_computed, 1)
        self.assertEqual(engine.stats.duplicate_steps_avoided, 1)

    def test_sample_world_sharing_statistic(self):
        facts, rules, goals = build_sample()
        engine = ProofEngine(facts, rules, goals, budget=7)
        GreedyScheduler(engine).run(exhaust=True)
        # g1/g2/g3 proof trees share r1 (net_ok): 3 goals, 7 unique steps.
        self.assertEqual(engine.stats.steps_computed, 7)
        self.assertEqual(engine.shared_work_avoided(), 2)


class DisproofTests(unittest.TestCase):
    def test_unsearched_is_not_disproof(self):
        facts, rules, goals = build_sample()
        engine = ProofEngine(facts, rules, goals, budget=2)
        GreedyScheduler(engine).run()
        # Budget spent, frontier still non-empty: g4 is unsearched, not disproved.
        self.assertTrue(engine.frontier)
        self.assertIs(engine.goal_status("g4"), GoalStatus.UNDECIDED)

    def test_disproof_only_after_exhaustion(self):
        facts, rules, goals = build_sample()
        engine = ProofEngine(facts, rules, goals, budget=7)
        GreedyScheduler(engine).run(exhaust=True)
        self.assertFalse(engine.frontier)
        self.assertIs(engine.goal_status("g4"), GoalStatus.DISPROVED)

    def test_unprovable_goal_disproved_immediately_when_no_rules_apply(self):
        facts = {"a": Fact("a")}
        rules = {"r1": Rule("r1", ("a",), "b")}
        goals = {"g": Goal("g", ("zzz",), 1.0)}
        engine = ProofEngine(facts, rules, goals, budget=5)
        engine.apply("r1")
        self.assertIs(engine.goal_status("g"), GoalStatus.DISPROVED)


class ConjunctionTests(unittest.TestCase):
    def test_conjunctive_goal_needs_all_targets(self):
        facts = {"a": Fact("a")}
        rules = {
            "r1": Rule("r1", ("a",), "b"),
            "r2": Rule("r2", ("a",), "c"),
        }
        goals = {"g": Goal("g", ("b", "c"), 1.0)}
        engine = ProofEngine(facts, rules, goals, budget=1)
        engine.apply("r1")
        self.assertIs(engine.goal_status("g"), GoalStatus.UNDECIDED)
        engine.add_budget(1)
        engine.apply("r2")
        self.assertIs(engine.goal_status("g"), GoalStatus.PROVED)


class RetractionTests(unittest.TestCase):
    def test_retraction_invalidates_and_recovers_budget(self):
        facts, rules, goals = tiny_world()
        engine = ProofEngine(facts, rules, goals, budget=3)
        GreedyScheduler(engine).run()
        self.assertEqual(engine.budget, 0)
        engine.retract_fact("a")
        self.assertEqual(engine.budget, 3)
        self.assertEqual(engine.stats.budget_recovered, 3)
        self.assertIs(engine.goal_status("g1"), GoalStatus.DISPROVED)
        self.assertIs(engine.goal_status("g2"), GoalStatus.DISPROVED)

    def test_stale_frontier_entries_reverified(self):
        facts, rules, goals = build_sample()
        engine = ProofEngine(facts, rules, goals, budget=3)
        GreedyScheduler(engine).run()  # applies r1, r2, r3
        self.assertTrue(engine.frontier)
        engine.retract_fact("obs_power")
        # r4 (needs svc_degraded) and r6 (needs net_ok) referenced evidence
        # that was invalidated; both must be re-verified.
        self.assertEqual(engine.stats.frontier_revalidations, 2)
        self.assertNotIn("r4", engine.frontier)
        self.assertNotIn("r6", engine.frontier)
        self.assertIn("r5", engine.frontier)  # thermal path unaffected

    def test_alternative_proof_reattached_for_free(self):
        facts, rules, goals = build_sample()
        engine = ProofEngine(facts, rules, goals, budget=7)
        GreedyScheduler(engine).run(exhaust=True)
        self.assertIs(engine.goal_status("g1"), GoalStatus.PROVED)
        steps_before = engine.stats.steps_computed
        engine.retract_fact("obs_power")
        # ticket_needed survives via the already-computed r5 derivation.
        self.assertIs(engine.goal_status("g1"), GoalStatus.PROVED)
        self.assertEqual(engine.stats.alternative_proofs_reused, 1)
        self.assertEqual(engine.stats.steps_computed, steps_before)

    def test_retracted_subproof_must_be_rederived(self):
        facts, rules, goals = tiny_world()
        engine = ProofEngine(facts, rules, goals, budget=3)
        GreedyScheduler(engine).run()
        engine.retract_fact("a")
        engine.update_fact(Fact("a", version=2))
        self.assertIs(engine.goal_status("g1"), GoalStatus.UNDECIDED)
        engine.add_budget(3)
        GreedyScheduler(engine).run()
        self.assertIs(engine.goal_status("g1"), GoalStatus.PROVED)
        self.assertEqual(engine.stats.steps_computed, 6)  # 3 old + 3 re-derived


class VersionTests(unittest.TestCase):
    def test_fact_version_bump_invalidates_dependents(self):
        facts, rules, goals = tiny_world()
        engine = ProofEngine(facts, rules, goals, budget=3)
        GreedyScheduler(engine).run()
        self.assertIs(engine.goal_status("g1"), GoalStatus.PROVED)
        engine.update_fact(Fact("a", version=2))
        self.assertIs(engine.goal_status("g1"), GoalStatus.UNDECIDED)
        self.assertEqual(engine.stats.budget_recovered, 3)

    def test_rule_version_bump_forces_recompute(self):
        facts, rules, goals = tiny_world()
        engine = ProofEngine(facts, rules, goals, budget=3)
        GreedyScheduler(engine).run()
        engine.update_rule(Rule("r2", ("b",), "c", version=2))
        self.assertIs(engine.goal_status("g1"), GoalStatus.UNDECIDED)
        self.assertIs(engine.goal_status("g2"), GoalStatus.PROVED)  # untouched
        self.assertEqual(engine.stats.budget_recovered, 1)
        engine.add_budget(1)
        engine.apply("r2")
        self.assertIs(engine.goal_status("g1"), GoalStatus.PROVED)


class CertificateTests(unittest.TestCase):
    def test_certificate_replays(self):
        facts, rules, goals = build_sample()
        engine = ProofEngine(facts, rules, goals, budget=7)
        GreedyScheduler(engine).run(exhaust=True)
        for gid in ("g1", "g2", "g3"):
            cert = engine.certificate(gid)
            self.assertTrue(replay_certificate(cert, engine.facts, engine.rules))

    def test_certificate_fails_on_version_tamper(self):
        facts, rules, goals = build_sample()
        engine = ProofEngine(facts, rules, goals, budget=7)
        GreedyScheduler(engine).run(exhaust=True)
        cert = engine.certificate("g1")
        tampered = dict(engine.facts)
        tampered["obs_power"] = Fact("obs_power", version=99)
        self.assertFalse(replay_certificate(cert, tampered, engine.rules))

    def test_certificate_fails_on_rule_version_tamper(self):
        facts, rules, goals = build_sample()
        engine = ProofEngine(facts, rules, goals, budget=7)
        GreedyScheduler(engine).run(exhaust=True)
        cert = engine.certificate("g1")
        tampered = dict(engine.rules)
        old = tampered["r4"]
        tampered["r4"] = Rule(old.rule_id, old.premises, old.conclusion, version=2)
        self.assertFalse(replay_certificate(cert, engine.facts, tampered))

    def test_certificate_requires_proved_goal(self):
        facts, rules, goals = tiny_world()
        engine = ProofEngine(facts, rules, goals, budget=0)
        with self.assertRaises(ValueError):
            engine.certificate("g1")


if __name__ == "__main__":
    unittest.main()
