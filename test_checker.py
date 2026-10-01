import unittest

from checker import Step, Trace, Verdict, analyze, replay
from model import ProtocolModel, RequestSystem, Transition
from samples import (
    build_blocked_protocol,
    build_bounded_protocol,
    build_retry_protocol,
)


class AnalyzeTests(unittest.TestCase):
    def test_may_complete_is_not_guaranteed(self):
        result = analyze(build_retry_protocol())
        self.assertEqual(result.verdict, Verdict.MAY_COMPLETE)
        self.assertIn("COMPLETED", result.terminals_reached)
        self.assertEqual(result.blocking_states["PROCESSING"], "livelock")

    def test_fair_livelock_states_reported(self):
        result = analyze(build_retry_protocol())
        self.assertEqual(
            {s for s, r in result.blocking_states.items() if r == "livelock"},
            {"PROCESSING", "RETRY_WAIT", "AWAITING_ACK"},
        )

    def test_counterexample_is_replayable_fair_lasso(self):
        model = build_retry_protocol()
        trace = analyze(model).counterexample
        self.assertIsNotNone(trace)
        self.assertTrue(trace.is_lasso)
        rep = replay(model, trace)
        self.assertTrue(rep.ok, rep.error)
        self.assertTrue(rep.loop_closed)
        self.assertTrue(rep.loop_fair)
        self.assertFalse(rep.is_terminal)

    def test_bounded_retries_must_complete(self):
        result = analyze(build_bounded_protocol())
        self.assertEqual(result.verdict, Verdict.MUST_COMPLETE)
        self.assertEqual(result.blocking_states, {})
        self.assertIsNone(result.counterexample)

    def test_blocked_when_no_terminal_reachable(self):
        model = build_blocked_protocol()
        result = analyze(model)
        self.assertEqual(result.verdict, Verdict.BLOCKED)
        self.assertEqual(result.blocking_states["AWAITING_DEP"], "dead")
        rep = replay(model, result.counterexample)
        self.assertTrue(rep.ok, rep.error)
        self.assertTrue(rep.loop_closed)
        self.assertFalse(rep.is_terminal)

    def test_unfair_spin_still_must_complete(self):
        model = ProtocolModel(
            name="spin",
            states={"S", "DONE"},
            terminals={"DONE"},
            initial="S",
            fair_groups=("spin", "progress"),
            transitions=[
                Transition("S", "spin", "S", "spin"),
                Transition("S", "finish", "DONE", "progress"),
            ],
        )
        # 无限自旋路径不公平(progress 持续可用却从不触发),不阻碍必完成
        self.assertEqual(analyze(model).verdict, Verdict.MUST_COMPLETE)

    def test_pending_when_search_truncated(self):
        result = analyze(build_retry_protocol(), max_states=3)
        self.assertEqual(result.verdict, Verdict.PENDING)
        self.assertFalse(result.explored_fully)
        self.assertIsNone(result.counterexample)

    def test_replay_rejects_invalid_step(self):
        model = build_retry_protocol()
        bad = Trace((Step("ACCEPTED", "teleport", "COMPLETED"),))
        rep = replay(model, bad)
        self.assertFalse(rep.ok)
        self.assertIsNotNone(rep.error)

    def test_replay_rejects_wrong_source(self):
        model = build_retry_protocol()
        bad = Trace((Step("QUEUED", "dispatch", "DISPATCHED"),))
        self.assertFalse(replay(model, bad).ok)


class RequestSystemTests(unittest.TestCase):
    def test_cancel_is_declared_terminal(self):
        model = build_retry_protocol()
        self.assertIn("CANCELLED", model.terminals)
        system = RequestSystem(model)
        key = system.accept("REQ-1")
        system.fire(key, "cancel")
        self.assertTrue(model.is_terminal(system.state_of(key)))

    def test_duplicate_request_ids_are_independent(self):
        system = RequestSystem(build_retry_protocol())
        key1 = system.accept("REQ-7")
        key2 = system.accept("REQ-7")
        self.assertNotEqual(key1, key2)
        system.fire(key1, "validate")
        self.assertEqual(system.state_of(key1), "VALIDATING")
        self.assertEqual(system.state_of(key2), "ACCEPTED")

    def test_success_path_does_not_imply_completion(self):
        result = analyze(build_retry_protocol())
        self.assertIn("COMPLETED", result.terminals_reached)
        self.assertNotEqual(result.verdict, Verdict.MUST_COMPLETE)


if __name__ == "__main__":
    unittest.main()
