"""“请求后最终响应”活性检查器的单元测试。"""
import unittest

from liveness import (
    Counterexample,
    FairnessSet,
    Model,
    Transition as T,
    Verdict,
    check,
    replay,
)

from examples import models


class TestSelfLoopCounterexample(unittest.TestCase):
    """自循环可构成反例。"""

    def test_self_loop_violation(self):
        model = models.deaf_server()
        result = check(model)
        self.assertEqual(result.verdict, Verdict.VIOLATED)
        cycle = result.counterexample.cycle
        self.assertEqual(len(cycle), 1)
        self.assertEqual(cycle[0].src, cycle[0].dst)  # 自循环
        self.assertEqual(cycle[0].action, "spin")

    def test_self_loop_counterexample_replays(self):
        model = models.deaf_server()
        result = check(model)
        report = replay(model, result.counterexample)
        self.assertTrue(report.ok, report.render())
        self.assertEqual(report.cycle_states, ("busy",))


class TestFairnessByExplicitActionSets(unittest.TestCase):
    """公平性只按显式动作集合定义，且假设变化须体现在证据中。"""

    def test_ignore_loop_violated_without_fairness(self):
        model = models.unreliable_server()
        result = check(model)
        self.assertEqual(result.verdict, Verdict.VIOLATED)
        self.assertEqual(result.counterexample.cycle[0].action, "ignore")

    def test_weak_fairness_excludes_ignore_loop(self):
        model = models.unreliable_server()
        fair = [FairnessSet("respond-progress", {"respond"}, kind="weak")]
        result = check(model, fairness=fair)
        self.assertEqual(result.verdict, Verdict.SATISFIED)

    def test_fairness_assumptions_echoed_in_result(self):
        model = models.unreliable_server()
        fair = [FairnessSet("respond-progress", {"respond"}, kind="weak")]
        result = check(model, fairness=fair)
        self.assertEqual(result.fairness_sets, tuple(fair))
        self.assertEqual(result.fairness_sets[0].actions, frozenset({"respond"}))

    def test_fairness_change_reflected_in_evidence(self):
        model = models.flaky_endpoint()
        weak = [FairnessSet("fix-weak", {"fix"}, kind="weak")]
        strong = [FairnessSet("fix-strong", {"fix"}, kind="strong")]
        weak_result = check(model, fairness=weak)
        strong_result = check(model, fairness=strong)
        # 同一模型，公平假设不同 -> 判定不同
        self.assertEqual(weak_result.verdict, Verdict.VIOLATED)
        self.assertEqual(strong_result.verdict, Verdict.SATISFIED)
        # 证据中体现各自假设：反例携带弱公平证据，重放报告逐条列出
        evidence = weak_result.counterexample.fairness
        self.assertEqual(len(evidence), 1)
        self.assertEqual(evidence[0].set_name, "fix-weak")
        self.assertEqual(evidence[0].kind, "weak")
        report = replay(model, weak_result.counterexample, fairness=weak)
        self.assertTrue(report.ok, report.render())
        self.assertEqual(report.fairness[0].set_name, "fix-weak")
        # 用强公平重放同一反例则被拒绝：假设变化直接改变证据结论
        strong_report = replay(
            model, weak_result.counterexample,
            fairness=[FairnessSet("fix-strong", {"fix"}, kind="strong")],
        )
        self.assertFalse(strong_report.ok)
        self.assertTrue(any("fix-strong" in p for p in strong_report.problems))

    def test_strong_fairness_semantics(self):
        # fix 仅在状态 a 可执行：循环 a->b->a 中它非持续可执行（弱公平放过），
        # 但无限次可执行（强公平要求执行）。
        model = models.flaky_endpoint()
        weak = check(model, fairness=[FairnessSet("w", {"fix"}, "weak")])
        strong = check(model, fairness=[FairnessSet("s", {"fix"}, "strong")])
        self.assertEqual(weak.verdict, Verdict.VIOLATED)
        self.assertEqual(strong.verdict, Verdict.SATISFIED)


class TestMultiStateCycle(unittest.TestCase):
    def test_lossy_channel_cycle(self):
        model = models.lossy_channel()
        result = check(model)
        self.assertEqual(result.verdict, Verdict.VIOLATED)
        cycle = result.counterexample.cycle
        self.assertGreaterEqual(len(cycle), 2)  # 多状态循环
        actions = [t.action for t in cycle]
        self.assertIn("dispatch", actions)
        self.assertIn("lost", actions)
        self.assertNotIn("delivered", actions)
        report = replay(model, result.counterexample)
        self.assertTrue(report.ok, report.render())

    def test_lossy_channel_weak_fairness_insufficient(self):
        # delivered 在 queued 状态不可执行，并非持续可执行，
        # 弱公平不能排除丢失循环。
        model = models.lossy_channel()
        fair = [FairnessSet("eventual-delivery", {"delivered"}, kind="weak")]
        result = check(model, fairness=fair)
        self.assertEqual(result.verdict, Verdict.VIOLATED)

    def test_lossy_channel_strong_fairness_satisfied(self):
        # 强公平：delivered 在 sending 上无限次可执行，必须被执行。
        model = models.lossy_channel()
        fair = [FairnessSet("eventual-delivery", {"delivered"}, kind="strong")]
        result = check(model, fairness=fair)
        self.assertEqual(result.verdict, Verdict.SATISFIED)


class TestTruncationIsPendingNotViolation(unittest.TestCase):
    """有限截断未见响应不能直接判违反；未搜索完保持未决。"""

    def test_depth_truncation_pending(self):
        model = models.slow_responder(n=6)
        result = check(model, max_depth=3)
        self.assertEqual(result.verdict, Verdict.PENDING)
        self.assertFalse(result.complete)
        self.assertIsNone(result.counterexample)
        self.assertTrue(result.notes)  # 截断须留痕

    def test_full_search_satisfied(self):
        model = models.slow_responder(n=6)
        result = check(model)
        self.assertEqual(result.verdict, Verdict.SATISFIED)
        self.assertTrue(result.complete)

    def test_state_limit_pending(self):
        model = models.slow_responder(n=6)
        result = check(model, max_states=2)
        self.assertEqual(result.verdict, Verdict.PENDING)

    def test_bad_cycle_beyond_depth_stays_pending(self):
        # 坏循环在深度限制之外：只能未决，不能判违反。
        model = Model(
            "deep-bad-cycle",
            initial="s0",
            transitions=[
                T("s0", "request", "s1"),
                T("s1", "step", "s2"),
                T("s2", "loop", "s3"),
                T("s3", "loop", "s2"),
            ],
            labels={"s0": set(), "s1": {"req"}, "s2": {"req"}, "s3": {"req"}},
        )
        result = check(model, max_depth=1)
        self.assertEqual(result.verdict, Verdict.PENDING)
        self.assertIsNone(result.counterexample)
        full = check(model)
        self.assertEqual(full.verdict, Verdict.VIOLATED)

    def test_prefix_only_trace_rejected_by_replay(self):
        # 有限轨迹（无循环）上未见到响应，不构成反例。
        model = models.slow_responder(n=6)
        prefix_only = Counterexample(
            prefix=(
                T("s0", "request", "s1"),
                T("s1", "work", "s2"),
            ),
            cycle=(),
            fairness=(),
        )
        report = replay(model, prefix_only)
        self.assertFalse(report.ok)
        self.assertTrue(any("cycle 为空" in p for p in report.problems))


class TestReplayVerification(unittest.TestCase):
    """反例必须可重放；篡改的轨迹必须被拒绝。"""

    def setUp(self):
        self.model = models.unreliable_server()
        self.result = check(self.model)
        assert self.result.verdict is Verdict.VIOLATED

    def test_valid_counterexample_replays(self):
        report = replay(self.model, self.result.counterexample)
        self.assertTrue(report.ok, report.render())

    def test_tampered_action_rejected(self):
        cex = self.result.counterexample
        bad_cycle = (T(cex.cycle[0].src, "respond", cex.cycle[0].dst),)
        tampered = Counterexample(cex.prefix, bad_cycle, cex.fairness)
        # respond 真实存在且会解除请求：cycle 上缺少 req 状态 -> 拒绝
        report = replay(self.model, tampered)
        self.assertFalse(report.ok)

    def test_nonexistent_transition_rejected(self):
        cex = self.result.counterexample
        bad_cycle = (T("busy", "vanish", "busy"),)
        tampered = Counterexample(cex.prefix, bad_cycle, cex.fairness)
        report = replay(self.model, tampered)
        self.assertFalse(report.ok)
        self.assertTrue(any("不存在迁移" in p for p in report.problems))

    def test_unclosed_cycle_rejected(self):
        model = models.lossy_channel()
        cex = Counterexample(
            prefix=(T("idle", "request", "queued"),),
            cycle=(T("queued", "dispatch", "sending"),),  # 不闭合
            fairness=(),
        )
        report = replay(model, cex)
        self.assertFalse(report.ok)
        self.assertTrue(any("不闭合" in p for p in report.problems))

    def test_cycle_with_response_rejected(self):
        model = models.unreliable_server()
        cex = Counterexample(
            prefix=(),
            cycle=(
                T("idle", "request", "busy"),
                T("busy", "respond", "served"),
                T("served", "reset", "idle"),
            ),
            fairness=(),
        )
        report = replay(model, cex)
        self.assertFalse(report.ok)
        self.assertTrue(any("response" in p for p in report.problems))


class TestReachability(unittest.TestCase):
    """验证对象限给定模型；不可达的坏循环不算反例。"""

    def test_unreachable_bad_loop_ignored(self):
        model = models.unreachable_bad_loop()
        result = check(model)
        self.assertEqual(result.verdict, Verdict.SATISFIED)
        self.assertEqual(result.explored_states, 3)  # zombie 未被探索

    def test_only_given_model_is_checked(self):
        # 模型中不存在的迁移不参与判定。
        model = Model(
            "minimal",
            initial="s",
            transitions=[T("s", "request", "s")],
            labels={"s": {"req"}},
        )
        result = check(model)
        self.assertEqual(result.verdict, Verdict.VIOLATED)
        report = replay(model, result.counterexample)
        self.assertTrue(report.ok, report.render())


class TestSatisfiedModels(unittest.TestCase):
    def test_immediate_response(self):
        model = Model(
            "eager",
            initial="idle",
            transitions=[
                T("idle", "request", "busy"),
                T("busy", "respond", "served"),
                T("served", "reset", "idle"),
            ],
            labels={"idle": set(), "busy": {"req"}, "served": {"resp"}},
        )
        result = check(model)
        self.assertEqual(result.verdict, Verdict.SATISFIED)
        self.assertIsNone(result.counterexample)

    def test_no_request_trivially_satisfied(self):
        model = Model(
            "quiet",
            initial="s",
            transitions=[T("s", "tick", "s")],
            labels={"s": set()},
        )
        result = check(model)
        self.assertEqual(result.verdict, Verdict.SATISFIED)


if __name__ == "__main__":
    unittest.main()
