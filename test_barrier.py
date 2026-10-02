import unittest

from barrier_model import (
    Model, RELEASE, WAITING, canon, enabled_actions, transitions, violations,
)
from model_checker import (
    LASSO, PENDING, SAFE, VIOLATION, ReplayError,
    check_liveness, check_safety, replay, replay_violation, verify_lasso,
)


class SafetyTests(unittest.TestCase):
    def test_correct_model_safe_with_closure_certificate(self):
        r = check_safety(Model(n=3, max_round=3))
        self.assertEqual(r.status, SAFE)
        self.assertTrue(r.certificate.startswith("closure sha256:"))

    def test_round_confusion_shortest_counterexample(self):
        # 旧轮到达计入新轮：BFS 给出最短反例，且可重放。
        model = Model(n=2, max_round=3, variant="round_confusion")
        r = check_safety(model)
        self.assertEqual(r.status, VIOLATION)
        self.assertIn("P2_stale_arrival", r.violated)
        self.assertEqual(len(r.trace), 4)  # arrive,arrive,release,release
        _, bad = replay_violation(model, r.trace)
        self.assertIn("P2_stale_arrival", bad)

    def test_stale_notification_counterexample(self):
        # 旧轮延迟通知唤醒新轮等待者。
        model = Model(n=2, max_round=3, variant="stale_notif")
        r = check_safety(model)
        self.assertEqual(r.status, VIOLATION)
        self.assertIn("P4_stale_notify", r.violated)
        _, bad = replay_violation(model, r.trace)
        self.assertIn("P4_stale_notify", bad)

    def test_stale_notify_never_wakes_in_correct_model(self):
        # 正确模型：上一轮延迟通知不能唤醒下一轮（P4 含于 SAFE）。
        r = check_safety(Model(n=2, max_round=2))
        self.assertEqual(r.status, SAFE)

    def test_at_most_one_release_per_round(self):
        # 最后一人到达与退出并发：每轮至多放行一次（P3 含于 SAFE），
        # 且所有可达状态满足 released_upto < rnd。
        r = check_safety(Model(n=3, max_round=3))
        self.assertEqual(r.status, SAFE)
        for s in r.explored:
            self.assertLess(s.released_upto, s.rnd)

    def test_no_release_before_quorum(self):
        # 同轮未齐不得放行：初始状态直接 release 不可重放。
        model = Model(n=2, max_round=2)
        with self.assertRaises(ReplayError):
            replay(model, [(RELEASE,)])

    def test_budget_exhaustion_stays_pending(self):
        # 预算不足不能称无错误。
        r = check_safety(Model(n=3, max_round=3), max_states=8)
        self.assertEqual(r.status, PENDING)
        self.assertIsNone(r.certificate)
        self.assertIsNone(r.trace)

    def test_dynamic_membership_actions_available(self):
        model = Model(n=2, max_round=2)
        acts = {a[0] for a in enabled_actions(model, model.initial_state())}
        self.assertIn("leave", acts)
        self.assertIn("arrive", acts)
        self.assertIn("disconnect", acts)


class LivenessTests(unittest.TestCase):
    def test_fair_lasso_replayable(self):
        model = Model(n=2, max_round=2)
        r = check_liveness(model)
        self.assertEqual(r.status, LASSO)
        self.assertTrue(verify_lasso(model, r.stem, r.loop))
        base = replay(model, r.stem)
        self.assertEqual(replay(model, r.loop, start=base), base)
        self.assertNotIn((RELEASE,), r.loop)

    def test_lasso_three_participants(self):
        model = Model(n=3, max_round=2)
        r = check_liveness(model)
        self.assertEqual(r.status, LASSO)
        self.assertTrue(verify_liveness := verify_lasso(model, r.stem, r.loop))

    def test_liveness_budget_pending(self):
        r = check_liveness(Model(n=3, max_round=3), max_states=5)
        self.assertEqual(r.status, PENDING)


class ReductionTests(unittest.TestCase):
    def test_reduction_matches_full_interleaving(self):
        # 小参与者全交错参考核验归约策略。
        for n in (2, 3):
            model = Model(n=n, max_round=2)
            full = check_safety(model, reduced=False)
            red = check_safety(model, reduced=True)
            self.assertEqual(full.status, red.status)
            self.assertEqual({canon(s) for s in full.explored}, set(red.explored))
            self.assertLessEqual(len(red.explored), len(full.explored))

    def test_reduction_preserves_buggy_verdict(self):
        model = Model(n=2, max_round=3, variant="round_confusion")
        full = check_safety(model, reduced=False)
        red = check_safety(model, reduced=True)
        self.assertEqual(full.status, VIOLATION)
        self.assertEqual(red.status, VIOLATION)


class ReplayTests(unittest.TestCase):
    def test_tampered_trace_rejected(self):
        model = Model(n=2, max_round=2)
        with self.assertRaises(ReplayError):
            replay(model, [("arrive", 0), (RELEASE,)])


if __name__ == "__main__":
    unittest.main()
