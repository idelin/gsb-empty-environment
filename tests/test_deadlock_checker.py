from __future__ import annotations

import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "src"))

from deadlock_checker import (  # noqa: E402
    Action, StateKind, Verdict, analyze, apply_action, classify_state,
    diagnose_deadlock, enabled_actions, is_enabled, make_model,
    replay_trace, verify_full_graph,
)


def resource_model(initial_states=(("I", "I"), ("H_A", "I"))):
    return make_model(
        name="资源死锁",
        participants=("P1", "P2"),
        local_states={"P1": ("I", "H_A", "D"), "P2": ("I", "H_B", "D")},
        actions=(
            Action("takeA", ("P1",), {"P1": "I"}, {"P1": "H_A"}),
            Action("takeB", ("P2",), {"P2": "I"}, {"P2": "H_B"}),
            Action("finishA", ("P1", "P2"),
                   {"P1": "H_A", "P2": "I"}, {"P1": "D", "P2": "D"}),
            Action("finishB", ("P2", "P1"),
                   {"P2": "H_B", "P1": "I"}, {"P2": "D", "P1": "D"}),
        ),
        initial_states=initial_states,
        terminal_states=(("D", "D"),),
    )


def no_provider_model():
    return make_model(
        name="无提供者",
        participants=("P1", "P2"),
        local_states={"P1": ("W",), "P2": ("X", "R")},
        actions=(
            Action("sync12", ("P1", "P2"),
                   {"P1": "W", "P2": "R"}, {"P1": "W", "P2": "X"}),
        ),
        initial_states=(("W", "X"),),
        terminal_states=(),
    )


def livelock_model():
    return make_model(
        name="活锁",
        participants=("P1",),
        local_states={"P1": ("W", "D")},
        actions=(Action("spin", ("P1",), {"P1": "W"}, {"P1": "W"}),),
        initial_states=(("W",), ("D",)),
        terminal_states=(("D",),),
    )


def terminal_violation_model():
    return make_model(
        name="终态违例",
        participants=("P1",),
        local_states={"P1": ("S", "T")},
        actions=(
            Action("toT", ("P1",), {"P1": "S"}, {"P1": "T"}),
            Action("leave", ("P1",), {"P1": "T"}, {"P1": "S"}),
        ),
        initial_states=(("S",),),
        terminal_states=(("T",),),
    )


def chain_model(length=30):
    states = tuple(f"S{i}" for i in range(length + 1))
    return make_model(
        name="长链",
        participants=("P1",),
        local_states={"P1": states},
        actions=tuple(
            Action(f"step{i}", ("P1",), {"P1": states[i]},
                   {"P1": states[i + 1]})
            for i in range(length)),
        initial_states=((states[0],),),
        terminal_states=(),
    )


class SemanticsTests(unittest.TestCase):

    def test_sync_enabled_only_when_all_preconditions_hold(self):
        model = resource_model()
        finish_a = next(a for a in model.actions if a.name == "finishA")
        self.assertTrue(is_enabled(model, ("H_A", "I"), finish_a))
        self.assertFalse(is_enabled(model, ("H_A", "H_B"), finish_a))

    def test_local_offer_is_not_global_enabled(self):
        model = resource_model()
        state = ("H_A", "H_B")
        enabled = {a.name for a in enabled_actions(model, state)}
        self.assertEqual(enabled, set())
        diagnosis = diagnose_deadlock(model, state)
        names = {b.name for b in diagnosis.blocking_actions}
        self.assertEqual(names, {"finishA", "finishB"})
        finish_a = next(b for b in diagnosis.blocking_actions
                        if b.name == "finishA")
        self.assertEqual(finish_a.offered_by, ("P1",))
        self.assertEqual(finish_a.missing, {"P2": "I"})

    def test_classification_rules(self):
        model = livelock_model()
        self.assertIs(classify_state(model, ("W",)),
                      StateKind.LIVELOCK_CANDIDATE)
        self.assertIs(classify_state(model, ("D",)),
                      StateKind.DECLARED_TERMINAL)

        tv = terminal_violation_model()
        self.assertIs(classify_state(tv, ("T",)),
                      StateKind.TERMINAL_NOT_STABLE)

        dead = no_provider_model()
        self.assertIs(classify_state(dead, ("W", "X")), StateKind.DEADLOCK)

    def test_normal_terminal_without_edges_is_not_deadlock(self):
        model = livelock_model()
        report = analyze(model)
        self.assertEqual(report.verdict, Verdict.PASS)
        self.assertIsNone(report.shortest_deadlock)

    def test_self_loop_is_livelock_not_deadlock(self):
        model = livelock_model()
        report = analyze(model)
        self.assertEqual(len(report.livelock_candidates), 1)
        self.assertEqual(report.livelock_candidates[0].state, ("W",))

    def test_apply_action_requires_enabled(self):
        model = resource_model()
        take_b = next(a for a in model.actions if a.name == "takeB")
        with self.assertRaises(ValueError):
            apply_action(model, ("H_A", "H_B"), take_b)
        self.assertEqual(apply_action(model, ("H_A", "I"), take_b),
                         ("H_A", "H_B"))


class DiagnosisTests(unittest.TestCase):

    def test_minimum_blocking_cycle(self):
        model = resource_model()
        diagnosis = diagnose_deadlock(model, ("H_A", "H_B"))
        self.assertIsNotNone(diagnosis.cycle)
        self.assertEqual(diagnosis.cycle.path, ("P1", "P2"))
        self.assertEqual(diagnosis.cycle.edges, ("finishA", "finishB"))
        self.assertIsNone(diagnosis.no_provider)

    def test_no_provider_evidence(self):
        model = no_provider_model()
        diagnosis = diagnose_deadlock(model, ("W", "X"))
        self.assertIsNone(diagnosis.cycle)
        self.assertIsNotNone(diagnosis.no_provider)
        self.assertEqual(diagnosis.no_provider.path, ("P1", "P2"))
        self.assertEqual(diagnosis.no_provider.sink, "P2")
        self.assertEqual(diagnosis.no_provider.edges, ("sync12",))

    def test_deadlock_without_any_blocking_action(self):
        model = chain_model(2)
        diagnosis = diagnose_deadlock(model, ("S2",))
        self.assertEqual(diagnosis.blocking_actions, ())
        self.assertIsNone(diagnosis.cycle)
        self.assertIsNone(diagnosis.no_provider)


class SearchAndMultiInitTests(unittest.TestCase):

    def test_deadlock_with_diagnosis_and_shortest_replayable_trace(self):
        model = resource_model()
        report = analyze(model)
        self.assertEqual(report.verdict, Verdict.FAIL_DEADLOCK)
        finding = report.shortest_deadlock
        self.assertEqual(finding.state, ("H_A", "H_B"))
        self.assertEqual(finding.depth, 1)
        self.assertEqual(finding.initial, ("H_A", "I"))
        replay = replay_trace(model, finding.initial, finding.trace)
        self.assertTrue(replay.ok)
        self.assertEqual(replay.final_state, finding.state)

    def test_all_initial_states_are_covered(self):
        model = resource_model()
        report = analyze(model)
        self.assertEqual(report.initials_covered, 2)
        self.assertEqual(report.initials_total, 2)
        self.assertTrue(all(r.covered for r in report.initial_results))

    def test_per_initial_shortest_trace(self):
        model = resource_model(initial_states=(("I", "I"), ("H_A", "I")))
        report = analyze(model)
        by_initial = {r.initial: r for r in report.initial_results}
        self.assertEqual(
            by_initial[("I", "I")].closest_deadlock.trace,
            ("takeA", "takeB"))
        self.assertEqual(
            by_initial[("H_A", "I")].closest_deadlock.trace, ("takeB",))

    def test_global_shortest_across_initial_states(self):
        model = resource_model()
        self.assertEqual(report_shortest_depth(model), 1)

    def test_terminal_violation(self):
        report = analyze(terminal_violation_model())
        self.assertEqual(report.verdict, Verdict.FAIL_TERMINAL_VIOLATION)
        self.assertEqual(report.shortest_terminal_violation.state, ("T",))

    def test_budget_exhaustion_is_undecided_not_terminal(self):
        model = chain_model(30)
        report = analyze(model, state_budget=5)
        self.assertEqual(report.budget_verdict, Verdict.UNDECIDED)
        self.assertTrue(report.budget_exhausted)
        self.assertEqual(report.initials_covered, 0)
        self.assertIsNone(report.shortest_deadlock)

    def test_budget_exactly_sufficient_is_decided(self):
        model = chain_model(2)
        report = analyze(model, state_budget=3)
        self.assertEqual(report.budget_verdict, Verdict.FAIL_DEADLOCK)
        self.assertFalse(report.budget_exhausted)

    def test_defect_found_before_budget_exhaustion_still_fails(self):
        model = resource_model()
        report = analyze(model, state_budget=100)
        self.assertEqual(report.budget_verdict, Verdict.FAIL_DEADLOCK)
        self.assertFalse(report.budget_exhausted)

    def test_initial_state_deadlock_depth_zero(self):
        report = analyze(no_provider_model())
        finding = report.shortest_deadlock
        self.assertEqual(finding.depth, 0)
        self.assertEqual(finding.trace, ())


class IndependentCheckTests(unittest.TestCase):

    def test_full_graph_check_agrees_on_shortest_and_replay(self):
        model = resource_model()
        report = analyze(model)
        check = report.full_check
        self.assertEqual(check.status, "complete")
        self.assertTrue(check.replay_ok)
        self.assertTrue(check.agreement)
        self.assertEqual(check.closest_deadlock.state, ("H_A", "H_B"))
        self.assertEqual(check.reachable_states, 5)
        self.assertEqual(check.edges, 6)

    def test_full_graph_reveals_hidden_deadlock_when_budget_runs_out(self):
        model = chain_model(30)
        report = analyze(model, state_budget=5)
        check = report.full_check
        self.assertEqual(check.status, "complete")
        self.assertIsNone(check.agreement)
        self.assertEqual(check.closest_deadlock.state, ("S30",))
        self.assertEqual(check.closest_deadlock.depth, 30)
        self.assertEqual(report.verdict, Verdict.FAIL_DEADLOCK)
        self.assertEqual(report.budget_verdict, Verdict.UNDECIDED)

    def test_full_graph_capacity_incomplete_keeps_undecided(self):
        model = chain_model(100)
        report = analyze(model, state_budget=5, full_check_cap=10)
        self.assertEqual(report.full_check.status, "incomplete")
        self.assertEqual(report.verdict, Verdict.UNDECIDED)
        self.assertIsNone(report.shortest_deadlock)

    def test_replay_detects_bogus_trace(self):
        model = resource_model()
        result = replay_trace(model, ("I", "I"), ("finishA",))
        self.assertFalse(result.ok)
        self.assertIn("不可执行", result.error)

    def test_full_check_rejects_forged_finding(self):
        model = resource_model()
        forged_type = report_shortest_deadlock_finding_type()
        finding = forged_type(StateKind.DEADLOCK, ("I", "I"),
                              ("H_A", "H_B"), 1, ("takeA",), None)
        check = verify_full_graph(model, (finding,))
        self.assertFalse(check.replay_ok)

    def test_passing_model_full_check_has_no_defects(self):
        model = livelock_model()
        report = analyze(model)
        check = report.full_check
        self.assertIsNone(check.closest_deadlock)
        self.assertIsNone(check.closest_terminal_violation)


class ValidationTests(unittest.TestCase):

    def test_reject_duplicate_initial_states(self):
        with self.assertRaises(ValueError):
            make_model(
                name="dup",
                participants=("P1",),
                local_states={"P1": ("A",)},
                actions=(),
                initial_states=(("A",), ("A",)),
                terminal_states=(),
            )

    def test_reject_action_precondition_not_covering_participants(self):
        with self.assertRaises(ValueError):
            make_model(
                name="badpre",
                participants=("P1", "P2"),
                local_states={"P1": ("I",), "P2": ("I",)},
                actions=(Action("a", ("P1", "P2"),
                                {"P1": "I"}, {"P1": "I"}),),
                initial_states=(("I", "I"),),
                terminal_states=(),
            )

    def test_reject_unknown_state_in_precondition(self):
        with self.assertRaises(ValueError):
            make_model(
                name="bad",
                participants=("P1",),
                local_states={"P1": ("A", "B")},
                actions=(Action("a", ("P1",), {"P1": "Z"}, {"P1": "B"}),),
                initial_states=(("A",),),
                terminal_states=(),
            )


def report_shortest_depth(model):
    return analyze(model).shortest_deadlock.depth


def report_shortest_deadlock_finding_type():
    from deadlock_checker import Finding
    return Finding


if __name__ == "__main__":
    unittest.main(verbosity=2)
