"""Tests for the partial-order-reduction model checker."""

from __future__ import annotations

import unittest

from porcheck import (
    PENDING,
    SAFE,
    UNSAFE,
    Action,
    Model,
    ReplayError,
    check,
    compare_with_full,
    independent,
    replay,
    validate_counterexample,
)
from examples import models


def make_action(name, reads, writes):
    return Action(
        name, frozenset(reads), frozenset(writes),
        lambda d: True, lambda d: d,
    )


class IndependenceTests(unittest.TestCase):
    def test_disjoint_actions_are_independent(self):
        a = make_action("a", {"x"}, {"x"})
        b = make_action("b", {"y"}, {"y"})
        self.assertTrue(independent(a, b))
        self.assertTrue(independent(b, a))

    def test_write_write_same_variable_is_not_independent(self):
        a = make_action("a", set(), {"x"})
        b = make_action("b", set(), {"x"})
        self.assertFalse(independent(a, b))
        self.assertFalse(independent(b, a))

    def test_write_read_conflict_is_not_independent(self):
        writer = make_action("w", set(), {"x"})
        reader = make_action("r", {"x"}, set())
        self.assertFalse(independent(writer, reader))
        self.assertFalse(independent(reader, writer))

    def test_read_read_sharing_is_independent(self):
        a = make_action("a", {"x"}, {"y"})
        b = make_action("b", {"x"}, {"z"})
        self.assertTrue(independent(a, b))

    def test_action_is_dependent_on_itself(self):
        a = make_action("a", {"x"}, {"x"})
        self.assertFalse(independent(a, a))


class ReductionTests(unittest.TestCase):
    def test_independent_counters_are_reduced(self):
        model = models.independent_counters(3, 4)
        por = check(model, reduction=True)
        full = check(model, reduction=False)
        self.assertEqual(por.verdict, SAFE)
        self.assertEqual(full.verdict, SAFE)
        self.assertEqual(por.states_explored, 3 + 4 + 1)
        self.assertEqual(full.states_explored, (3 + 1) * (4 + 1))
        self.assertLess(por.states_explored, full.states_explored)

    def test_shared_counter_is_not_reduced(self):
        model = models.shared_counter(2)
        por = check(model, reduction=True)
        full = check(model, reduction=False)
        self.assertEqual(por.verdict, SAFE)
        self.assertEqual(por.states_explored, full.states_explored)
        self.assertEqual(por.evidence_summary()["singleton_reductions"], 0)

    def test_reduction_evidence_is_recorded(self):
        model = models.independent_counters(2, 3)
        result = check(model, reduction=True)
        self.assertTrue(result.decisions)
        for decision in result.decisions:
            self.assertEqual(len(decision.chosen), 1)
            self.assertIn("independent", decision.reason)
        summary = result.evidence_summary()
        self.assertEqual(
            summary["states_expanded"],
            summary["singleton_reductions"]
            + summary["cycle_proviso_full_expansions"]
            + summary["other_full_expansions"],
        )


class SafetyTests(unittest.TestCase):
    def test_invariant_violation_is_found_and_replays(self):
        model = models.copy_violation(2)
        for reduction in (True, False):
            result = check(model, reduction=reduction)
            self.assertEqual(result.verdict, UNSAFE)
            self.assertIn("y_small", result.violation)
            final = replay(model, result.trace)
            self.assertEqual(dict(final)["y"], 2)
            self.assertTrue(
                validate_counterexample(model, result.trace, result.violation)
            )

    def test_safe_invariant_holds(self):
        model = models.shared_counter(3)
        result = check(model, reduction=True)
        self.assertEqual(result.verdict, SAFE)
        self.assertIsNone(result.trace)

    def test_tampered_trace_is_rejected(self):
        model = models.copy_violation(2)
        result = check(model, reduction=True)
        trace = result.trace
        trace.actions[0] = "copy"  # not enabled in the initial state
        with self.assertRaises(ReplayError):
            replay(model, trace)

    def test_unknown_action_in_trace_is_rejected(self):
        model = models.copy_violation(2)
        result = check(model, reduction=True)
        trace = result.trace
        trace.actions[-1] = "nonexistent"
        with self.assertRaises(ReplayError):
            replay(model, trace)


class DeadlockTests(unittest.TestCase):
    def test_lock_deadlock_is_preserved_under_reduction(self):
        model = models.lock_deadlock()
        por = check(model, reduction=True)
        full = check(model, reduction=False)
        self.assertEqual(por.verdict, UNSAFE)
        self.assertEqual(full.verdict, UNSAFE)
        self.assertTrue(por.violation.startswith("deadlock"))
        final = replay(model, por.trace)
        self.assertEqual(
            dict(final), {"la": 1, "lb": 2, "pc1": 1, "pc2": 1}
        )
        self.assertTrue(
            validate_counterexample(model, por.trace, por.violation)
        )

    def test_deadlock_at_normal_termination_is_found(self):
        model = models.independent_counters(2, 2, deadlock_is_error=True)
        por = check(model, reduction=True)
        self.assertEqual(por.verdict, UNSAFE)
        self.assertTrue(por.violation.startswith("deadlock"))
        self.assertEqual(dict(replay(model, por.trace)), {"x": 2, "y": 2})

    def test_non_error_deadlock_does_not_block_search(self):
        model = models.independent_counters(2, 2, deadlock_is_error=False)
        result = check(model, reduction=True)
        self.assertEqual(result.verdict, SAFE)


class CycleProvisoTests(unittest.TestCase):
    def test_proviso_protects_ignored_action_on_cycle(self):
        model = models.cycle_ignoring()
        guarded = check(model, reduction=True, cycle_proviso=True)
        full = check(model, reduction=False)
        self.assertEqual(guarded.verdict, UNSAFE)
        self.assertEqual(full.verdict, UNSAFE)
        self.assertTrue(
            validate_counterexample(model, guarded.trace, guarded.violation)
        )
        proviso_decisions = [
            d for d in guarded.decisions if d.proviso_triggered
        ]
        self.assertTrue(proviso_decisions)
        self.assertIn("cycle proviso", proviso_decisions[0].reason)

    def test_without_proviso_the_violation_is_missed(self):
        # Documents the danger the proviso guards against: `set_y` is
        # ignored on the flip cycle and the search wrongly reports safe.
        model = models.cycle_ignoring()
        naive = check(model, reduction=True, cycle_proviso=False)
        self.assertEqual(naive.verdict, SAFE)


class PendingTests(unittest.TestCase):
    def test_unfinished_search_stays_pending(self):
        model = models.independent_counters(50, 50)
        for reduction in (True, False):
            result = check(model, reduction=reduction, max_states=100)
            self.assertEqual(result.verdict, PENDING)
            self.assertTrue(result.truncated)
            self.assertEqual(result.states_explored, 100)
            self.assertIsNone(result.trace)

    def test_violation_before_budget_is_conclusive(self):
        model = models.copy_violation(2)
        result = check(model, reduction=True, max_states=1000)
        self.assertEqual(result.verdict, UNSAFE)


class ComparisonTests(unittest.TestCase):
    def test_por_agrees_with_full_enumeration_on_small_models(self):
        small_models = [
            models.independent_counters(1, 1),
            models.independent_counters(2, 3),
            models.independent_counters(2, 2, deadlock_is_error=True),
            models.shared_counter(1),
            models.shared_counter(3),
            models.copy_violation(1),
            models.copy_violation(3),
            models.lock_deadlock(),
            models.cycle_ignoring(),
        ]
        for model in small_models:
            with self.subTest(model=model.name):
                cmp = compare_with_full(model)
                self.assertTrue(cmp.conclusive)
                self.assertTrue(cmp.verdicts_agree)
                if cmp.por.verdict == SAFE:
                    self.assertLessEqual(
                        cmp.por.states_explored, cmp.full.states_explored
                    )
                if cmp.por.trace is not None:
                    self.assertTrue(
                        validate_counterexample(
                            model, cmp.por.trace, cmp.por.violation
                        )
                    )


class ValidationTests(unittest.TestCase):
    def test_undeclared_write_is_rejected(self):
        bad = Action(
            "bad", frozenset(), frozenset({"x"}),
            lambda d: True,
            lambda d: {**d, "x": 1, "y": 1},
        )
        model = Model("bad_model", {"x": 0, "y": 0}, (bad,))
        with self.assertRaises(ValueError):
            check(model)


if __name__ == "__main__":
    unittest.main()
