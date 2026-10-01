import unittest

from fairness_checker import (
    Enablement,
    FairnessKind,
    TransitionSystem,
    Verdict,
    Witness,
    check_fairness,
    replay,
)


def model_wf():
    # 'a' continuously enabled on the t0 self-loop, but the loop can avoid it.
    return TransitionSystem("t0", {
        "t0": [("b", "t0"), ("a", "t1")],
        "t1": [("b", "t0")],
    })


def model_sf():
    # 'a' enabled only in s0: intermittent on the s0<->s1 loop.
    return TransitionSystem("s0", {
        "s0": [("b", "s1"), ("a", "s2")],
        "s1": [("b", "s0")],
        "s2": [("b", "s2")],
    })


class WeakFairnessTests(unittest.TestCase):
    def test_weak_violated_by_continuous_enablement(self):
        result = check_fairness(model_wf(), weak=("a",))
        r = result.for_action("a", FairnessKind.WEAK)
        self.assertIs(r.verdict, Verdict.VIOLATED)
        self.assertIs(r.witness.enablement, Enablement.CONTINUOUS)
        self.assertTrue(all(a != "a" for _, a, _ in r.witness.loop))

    def test_weak_not_violated_by_intermittent_enablement(self):
        # Intermittent enablement must NOT be reported as a weak-fairness
        # violation: the two enablement kinds are distinct.
        result = check_fairness(model_sf(), weak=("a",))
        r = result.for_action("a", FairnessKind.WEAK)
        self.assertIs(r.verdict, Verdict.SATISFIED)
        self.assertIsNone(r.witness)

    def test_weak_satisfied_when_action_executed(self):
        model = TransitionSystem("u0", {
            "u0": [("a", "u1"), ("b", "u1")],
            "u1": [("a", "u0")],
        })
        result = check_fairness(model, weak=("a",))
        self.assertIs(result.verdict, Verdict.SATISFIED)

    def test_unreachable_violating_loop_is_ignored(self):
        # The a-enabled loop at x0 is unreachable from the initial state.
        model = TransitionSystem("r0", {
            "r0": [("b", "r0")],
            "x0": [("b", "x0"), ("a", "x1")],
            "x1": [("b", "x0")],
        })
        result = check_fairness(model, weak=("a",))
        self.assertIs(result.verdict, Verdict.SATISFIED)
        self.assertEqual(result.explored, 1)


class StrongFairnessTests(unittest.TestCase):
    def test_strong_violated_by_intermittent_enablement(self):
        result = check_fairness(model_sf(), strong=("a",))
        r = result.for_action("a", FairnessKind.STRONG)
        self.assertIs(r.verdict, Verdict.VIOLATED)
        self.assertIs(r.witness.enablement, Enablement.INTERMITTENT)

    def test_strong_satisfied_when_never_enabled_in_any_loop(self):
        # 'a' fires once on the way out of s0; the only loop (s1) never enables it.
        model = TransitionSystem("s0", {
            "s0": [("a", "s1"), ("b", "s1")],
            "s1": [("b", "s1")],
        })
        result = check_fairness(model, strong=("a",))
        self.assertIs(result.verdict, Verdict.SATISFIED)

    def test_strong_violated_by_continuous_enablement_too(self):
        result = check_fairness(model_wf(), strong=("a",))
        r = result.for_action("a", FairnessKind.STRONG)
        self.assertIs(r.verdict, Verdict.VIOLATED)
        self.assertIs(r.witness.enablement, Enablement.CONTINUOUS)


class PendingTests(unittest.TestCase):
    def test_truncated_search_is_pending_not_satisfied(self):
        result = check_fairness(model_wf(), weak=("a",), max_states=1)
        self.assertFalse(result.complete)
        r = result.for_action("a", FairnessKind.WEAK)
        self.assertIs(r.verdict, Verdict.PENDING)
        self.assertIs(result.verdict, Verdict.PENDING)

    def test_sufficient_limit_completes(self):
        result = check_fairness(model_wf(), weak=("a",), max_states=2)
        self.assertTrue(result.complete)
        self.assertIs(result.verdict, Verdict.VIOLATED)


class ReplayTests(unittest.TestCase):
    def test_replay_confirms_witness_and_loop_repeats(self):
        result = check_fairness(model_wf(), weak=("a",))
        w = result.for_action("a", FairnessKind.WEAK).witness
        report = replay(model_wf(), w, iterations=3)
        self.assertTrue(report.ok)
        self.assertTrue(report.violation_confirmed)
        self.assertEqual(report.iterations_checked, 3)
        self.assertIs(report.enablement, Enablement.CONTINUOUS)
        self.assertFalse(report.executed_in_loop)

    def test_replay_rejects_tampered_transition(self):
        result = check_fairness(model_wf(), weak=("a",))
        w = result.for_action("a", FairnessKind.WEAK).witness
        bad_loop = ((w.loop[0][0], "z", w.loop[0][2]),) + w.loop[1:]
        tampered = Witness(w.action, w.kind, w.prefix, bad_loop, w.enablement)
        report = replay(model_wf(), tampered)
        self.assertFalse(report.ok)
        self.assertIn("not a transition", report.error)

    def test_replay_rejects_non_closing_loop(self):
        model = model_wf()
        # Hand-built witness whose loop does not return to its start state.
        w = Witness("a", FairnessKind.WEAK,
                    prefix=(),
                    loop=(("t0", "a", "t1"), ("t1", "b", "t0"), ("t0", "a", "t1")),
                    enablement=Enablement.INTERMITTENT)
        report = replay(model, w)
        self.assertFalse(report.ok)
        self.assertIn("does not close", report.error)

    def test_replay_rejects_wrong_prefix_source(self):
        result = check_fairness(model_wf(), weak=("a",))
        w = result.for_action("a", FairnessKind.WEAK).witness
        bad_prefix = (("t1", "b", "t0"),)
        tampered = Witness(w.action, w.kind, bad_prefix, w.loop, w.enablement)
        report = replay(model_wf(), tampered)
        self.assertFalse(report.ok)

    def test_replay_rederives_enablement_not_witness_claim(self):
        # A witness that lies about its classification is still judged by the model.
        result = check_fairness(model_sf(), strong=("a",))
        w = result.for_action("a", FairnessKind.STRONG).witness
        lying = Witness(w.action, w.kind, w.prefix, w.loop, Enablement.CONTINUOUS)
        report = replay(model_sf(), lying)
        self.assertTrue(report.ok)
        self.assertIs(report.enablement, Enablement.INTERMITTENT)
        self.assertTrue(report.violation_confirmed)


if __name__ == "__main__":
    unittest.main()
