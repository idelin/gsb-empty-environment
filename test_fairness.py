"""Unit tests for the fair-cycle witness checker (stdlib unittest only)."""

import unittest

from fairness import (
    FairnessKind,
    Verdict,
    Witness,
    check_fairness,
    replay,
    TransitionSystem,
)


def worker_model():
    return TransitionSystem(
        initials=["idle"],
        transitions=[
            ("idle", "work", "busy"),
            ("busy", "work", "idle"),
            ("idle", "reset", "idle"),
            ("busy", "reset", "busy"),
        ],
    )


def pingpong_model():
    return TransitionSystem(
        initials=["s0"],
        transitions=[
            ("s0", "ping", "s1"),
            ("s1", "pong", "s0"),
            ("s0", "recover", "s0"),
        ],
    )


def toggle_model():
    return TransitionSystem(
        initials=["off"],
        transitions=[
            ("off", "step", "on"),
            ("on", "reset", "off"),
        ],
    )


class WeakFairnessTests(unittest.TestCase):
    def test_continuous_enablement_violates_weak_fairness(self):
        ts = worker_model()
        (report,) = check_fairness(ts, [("reset", FairnessKind.WEAK)])
        self.assertIs(report.verdict, Verdict.VIOLATED)
        stats = report.witness.stats
        self.assertTrue(stats.continuously_enabled)
        self.assertFalse(stats.intermittently_enabled)
        self.assertFalse(stats.taken_in_cycle)
        self.assertEqual(len(stats.enabled_at), len(stats.cycle_states))

    def test_intermittent_enablement_is_not_a_weak_violation(self):
        ts = pingpong_model()
        (report,) = check_fairness(ts, [("recover", FairnessKind.WEAK)])
        self.assertIs(report.verdict, Verdict.HOLDS)
        self.assertIsNone(report.witness)
        self.assertTrue(report.search_exhausted)

    def test_weak_witness_never_takes_action_in_cycle(self):
        ts = worker_model()
        (report,) = check_fairness(ts, [("reset", FairnessKind.WEAK)])
        cycle_actions = [act for _, act, _ in report.witness.cycle]
        self.assertNotIn("reset", cycle_actions)

    def test_self_loop_witness(self):
        ts = TransitionSystem(
            initials=["s"],
            transitions=[("s", "a", "s"), ("s", "b", "s")],
        )
        (report,) = check_fairness(ts, [("b", FairnessKind.WEAK)])
        self.assertIs(report.verdict, Verdict.VIOLATED)
        self.assertEqual(len(report.witness.cycle), 1)
        self.assertTrue(replay(ts, report.witness).ok)


class StrongFairnessTests(unittest.TestCase):
    def test_intermittent_enablement_violates_strong_fairness(self):
        ts = pingpong_model()
        (report,) = check_fairness(ts, [("recover", FairnessKind.STRONG)])
        self.assertIs(report.verdict, Verdict.VIOLATED)
        stats = report.witness.stats
        self.assertTrue(stats.intermittently_enabled)
        self.assertFalse(stats.continuously_enabled)
        self.assertFalse(stats.taken_in_cycle)

    def test_continuous_enablement_also_violates_strong_fairness(self):
        ts = worker_model()
        (report,) = check_fairness(ts, [("reset", FairnessKind.STRONG)])
        self.assertIs(report.verdict, Verdict.VIOLATED)
        # Recorded as continuous, not mislabeled as intermittent.
        self.assertTrue(report.witness.stats.continuously_enabled)
        self.assertFalse(report.witness.stats.intermittently_enabled)


class HoldsAndPendingTests(unittest.TestCase):
    def test_holds_when_every_cycle_takes_the_action(self):
        ts = toggle_model()
        reports = check_fairness(
            ts, [("reset", FairnessKind.WEAK), ("reset", FairnessKind.STRONG)])
        for report in reports:
            self.assertIs(report.verdict, Verdict.HOLDS)
            self.assertIsNone(report.witness)
            self.assertTrue(report.search_exhausted)

    def test_pending_when_search_is_incomplete(self):
        ts = worker_model()
        (report,) = check_fairness(ts, [("reset", FairnessKind.WEAK)],
                                   max_states=1)
        self.assertIs(report.verdict, Verdict.PENDING)
        self.assertIsNone(report.witness)
        self.assertFalse(report.search_exhausted)

    def test_unreachable_cycle_is_ignored(self):
        ts = TransitionSystem(
            initials=["a"],
            transitions=[
                ("a", "g", "a"),
                # Unreachable component with a weak-fairness-violating loop.
                ("u", "p", "v"),
                ("v", "p", "u"),
                ("u", "f", "u"),
                ("v", "f", "v"),
            ],
        )
        (report,) = check_fairness(ts, [("f", FairnessKind.WEAK)])
        self.assertIs(report.verdict, Verdict.HOLDS)
        self.assertEqual(report.states_explored, 1)


class ReplayTests(unittest.TestCase):
    def test_replay_verifies_weak_witness(self):
        ts = worker_model()
        (report,) = check_fairness(ts, [("reset", FairnessKind.WEAK)])
        result = replay(ts, report.witness)
        self.assertTrue(result.ok, result.error)
        self.assertEqual(result.visited[-1], report.witness.cycle[0][0])

    def test_replay_verifies_strong_witness(self):
        ts = pingpong_model()
        (report,) = check_fairness(ts, [("recover", FairnessKind.STRONG)])
        self.assertTrue(replay(ts, report.witness).ok)

    def test_replay_rejects_tampered_transition(self):
        ts = worker_model()
        (report,) = check_fairness(ts, [("reset", FairnessKind.WEAK)])
        witness = report.witness
        src, act, _ = witness.cycle[0]
        tampered = Witness(
            action=witness.action,
            kind=witness.kind,
            stem=witness.stem,
            cycle=[(src, act, "nowhere")] + witness.cycle[1:],
            stats=witness.stats,
        )
        result = replay(ts, tampered)
        self.assertFalse(result.ok)
        self.assertIn("invalid transition", result.error)

    def test_replay_rejects_confused_fairness_kind(self):
        # An intermittently-enabled strong-fairness witness must not pass
        # as a weak-fairness witness: the two patterns stay distinct.
        ts = pingpong_model()
        (report,) = check_fairness(ts, [("recover", FairnessKind.STRONG)])
        witness = report.witness
        confused = Witness(
            action=witness.action,
            kind=FairnessKind.WEAK,
            stem=witness.stem,
            cycle=witness.cycle,
            stats=witness.stats,
        )
        result = replay(ts, confused)
        self.assertFalse(result.ok)
        self.assertIn("continuous", result.error)

    def test_replay_rejects_cycle_taking_the_action(self):
        ts = toggle_model()
        bogus = Witness(
            action="reset",
            kind=FairnessKind.STRONG,
            stem=[],
            cycle=[("off", "step", "on"), ("on", "reset", "off")],
            stats=None,
        )
        result = replay(ts, bogus)
        self.assertFalse(result.ok)
        self.assertIn("taken inside the cycle", result.error)


if __name__ == "__main__":
    unittest.main()
