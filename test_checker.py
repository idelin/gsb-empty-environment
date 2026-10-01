"""Unit tests for the request/response liveness checker.

Run with: python3.11 -m unittest -v
"""
from __future__ import annotations

import unittest

from checker import Counterexample, Step, Verdict, check, replay
from examples import deep_service, lossy_service, reliable_service
from fsm import Model


def spin_only_service() -> Model:
    """A request state whose only outgoing edge is a self-loop."""
    model = Model("spin-only", "idle")
    model.add_state("idle")
    model.add_state("wait", labels={"req"})
    model.add_transition("idle", "request", "wait")
    model.add_transition("wait", "spin", "wait")
    return model


class TestVerdicts(unittest.TestCase):
    def test_reliable_service_is_satisfied(self):
        result = check(reliable_service())
        self.assertIs(result.verdict, Verdict.SATISFIED)
        self.assertFalse(result.evidence.truncated)
        self.assertIsNone(result.evidence.counterexample)

    def test_lossy_service_is_violated(self):
        result = check(lossy_service())
        self.assertIs(result.verdict, Verdict.VIOLATED)
        self.assertIsNotNone(result.evidence.counterexample)

    def test_self_loop_alone_is_a_counterexample(self):
        result = check(spin_only_service())
        self.assertIs(result.verdict, Verdict.VIOLATED)
        loop = result.evidence.counterexample.loop
        self.assertEqual(len(loop), 1)
        self.assertEqual(loop[0], Step("spin", "wait", True))

    def test_truncated_search_stays_pending(self):
        result = check(deep_service(), max_nodes=4)
        self.assertIs(result.verdict, Verdict.PENDING)
        self.assertTrue(result.evidence.truncated)
        self.assertIsNone(result.evidence.counterexample)

    def test_truncation_without_response_is_not_a_violation(self):
        result = check(deep_service(), max_nodes=4)
        self.assertIsNot(result.verdict, Verdict.VIOLATED)

    def test_full_search_resolves_pending(self):
        self.assertIs(check(deep_service(), max_nodes=4).verdict, Verdict.PENDING)
        self.assertIs(check(deep_service()).verdict, Verdict.VIOLATED)


class TestFairness(unittest.TestCase):
    def test_fairness_on_respond_excludes_spin_loop(self):
        result = check(lossy_service(), fairness_sets=[{"respond"}])
        self.assertIs(result.verdict, Verdict.SATISFIED)
        self.assertIsNone(result.evidence.counterexample)

    def test_unlisted_actions_get_no_fairness_assumption(self):
        result = check(lossy_service(), fairness_sets=[{"spin"}])
        self.assertIs(result.verdict, Verdict.VIOLATED)

    def test_fairness_assumptions_are_recorded_in_evidence(self):
        plain = check(lossy_service())
        fair = check(lossy_service(), fairness_sets=[{"respond"}])
        self.assertEqual(plain.evidence.fairness_sets, [])
        self.assertEqual(fair.evidence.fairness_sets, [frozenset({"respond"})])
        self.assertEqual(plain.evidence.ignored_unfair_components, [])
        self.assertEqual(len(fair.evidence.ignored_unfair_components), 1)
        ignored = fair.evidence.ignored_unfair_components[0]
        self.assertEqual(ignored.states, ["wait"])
        self.assertEqual(ignored.missing_fairness, [frozenset({"respond"})])
        self.assertTrue(ignored.sample_loop)

    def test_fair_counterexample_still_violates(self):
        model = lossy_service()
        result = check(model, fairness_sets=[{"spin"}])
        self.assertIs(result.verdict, Verdict.VIOLATED)
        report = replay(model, result.evidence.counterexample,
                        fairness_sets=[{"spin"}])
        self.assertTrue(report.ok, report.format())


class TestReplay(unittest.TestCase):
    def test_counterexample_replays_cleanly(self):
        model = lossy_service()
        result = check(model)
        report = replay(model, result.evidence.counterexample)
        self.assertTrue(report.ok, report.format())

    def test_replay_rejects_tampered_action(self):
        model = lossy_service()
        cex = check(model).evidence.counterexample
        forged_step = Step("teleport", cex.loop[0].state, cex.loop[0].pending)
        tampered = Counterexample(cex.prefix, [forged_step])
        self.assertFalse(replay(model, tampered).ok)

    def test_replay_rejects_tampered_pending_flag(self):
        model = lossy_service()
        cex = check(model).evidence.counterexample
        forged_step = Step(cex.loop[0].action, cex.loop[0].state, False)
        tampered = Counterexample(cex.prefix, [forged_step])
        self.assertFalse(replay(model, tampered).ok)

    def test_replay_enforces_declared_fairness(self):
        model = lossy_service()
        cex = check(model).evidence.counterexample
        self.assertTrue(replay(model, cex).ok)
        report = replay(model, cex, fairness_sets=[{"respond"}])
        self.assertFalse(report.ok)


if __name__ == "__main__":
    unittest.main()
