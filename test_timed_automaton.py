"""timed_automaton 的 unittest 测试套件。"""

import unittest

from timed_automaton import (
    Constraint,
    Delay,
    ReplayError,
    Take,
    TimedAutomaton,
    Transition,
    Verdict,
    check_reachability,
    format_trajectory,
    replay,
)
from example import TIME_BOUND, build_model


def single_clock(*transitions, invariants=None):
    return TimedAutomaton(
        clocks=("x",),
        initial="s0",
        transitions=tuple(transitions),
        invariants=invariants or {},
    )


class StrictnessTest(unittest.TestCase):
    """严格 x<3 与非严格 x<=3 语义必须不同。"""

    def _model(self, op):
        return single_clock(
            Transition("wait_enough", "s0", "s1", guard=(Constraint("x", ">=", 3),)),
            Transition("finish", "s1", "goal", guard=(Constraint("x", op, 3),)),
        )

    def test_strict_less_than_unreachable_at_boundary(self):
        # x>=3 之后 x<3 永不成立(无 reset), 状态空间穷尽 -> UNREACHABLE
        result = check_reachability(self._model("<"), "goal", time_bound=10)
        self.assertIs(result.verdict, Verdict.UNREACHABLE)

    def test_non_strict_reachable_at_boundary(self):
        # x<=3 在 x=3 处成立 -> REACHABLE, 且轨迹恰在 t=3 触发
        result = check_reachability(self._model("<="), "goal", time_bound=10)
        self.assertIs(result.verdict, Verdict.REACHABLE)
        snapshots = replay(self._model("<="), result.trace, time_bound=10)
        self.assertEqual(snapshots[-1][0], 3)
        self.assertEqual(snapshots[-1][1], "goal")

    def test_strict_bound_excludes_exact_value(self):
        # guard x<3: 延迟 3 后不可触发, 延迟 2 可触发
        model = single_clock(
            Transition("go", "s0", "goal", guard=(Constraint("x", "<", 3),))
        )
        with self.assertRaises(ReplayError):
            replay(model, [Delay(3), Take("go")], time_bound=10)
        snapshots = replay(model, [Delay(2), Take("go")], time_bound=10)
        self.assertEqual(snapshots[-1][1], "goal")


class ResetTest(unittest.TestCase):
    """reset 后旧时钟值不保留。"""

    def _model(self, resets):
        return single_clock(
            Transition(
                "first", "s0", "s1",
                guard=(Constraint("x", ">=", 2),), resets=frozenset(resets),
            ),
            Transition("second", "s1", "goal", guard=(Constraint("x", "<", 1),)),
        )

    def test_reset_clears_old_value(self):
        # x>=2 后 reset x -> x=0 < 1 成立
        model = self._model(resets={"x"})
        result = check_reachability(model, "goal", time_bound=10)
        self.assertIs(result.verdict, Verdict.REACHABLE)
        snapshots = replay(model, result.trace, time_bound=10)
        self.assertEqual(snapshots[-1][2]["x"], 0)

    def test_without_reset_old_value_retained(self):
        # 无 reset: x>=2 与 x<1 矛盾 -> UNREACHABLE
        result = check_reachability(self._model(resets=set()), "goal", time_bound=10)
        self.assertIs(result.verdict, Verdict.UNREACHABLE)


class ZeroTimeLoopTest(unittest.TestCase):
    """零时间环不能无限扩展, 搜索必须终止。"""

    def test_zero_time_self_loop_terminates(self):
        model = single_clock(
            Transition("spin", "s0", "s0", guard=(Constraint("x", "<=", 100),)),
            Transition("exit", "s0", "goal", guard=(Constraint("x", ">=", 2),)),
        )
        result = check_reachability(model, "goal", time_bound=10)
        self.assertIs(result.verdict, Verdict.REACHABLE)
        # K=100, 截断阈值 101: s0 的状态数 <= 101, 加 goal 共 <= 102
        self.assertLessEqual(result.explored_states, 102)

    def test_reset_self_loop_is_single_state(self):
        # 带 reset 的零时间自环: (s0, x=0) 唯一状态, 立即被 visited 吸收
        model = single_clock(
            Transition("spin", "s0", "s0", resets=frozenset({"x"})),
            Transition("exit", "s0", "goal", guard=(Constraint("x", ">=", 3),)),
        )
        result = check_reachability(model, "goal", time_bound=10)
        self.assertIs(result.verdict, Verdict.REACHABLE)


class TruncationTest(unittest.TestCase):
    """等价截断: 超过 K+1 的时钟值合并, 状态空间有限。"""

    def test_state_space_bounded_by_cap(self):
        model = single_clock(
            Transition("exit", "s0", "goal", guard=(Constraint("x", ">=", 3),))
        )
        result = check_reachability(model, "goal", time_bound=1000)
        self.assertIs(result.verdict, Verdict.REACHABLE)
        # K=3, 截断阈值 4: s0 取值 {0,1,2,3,4} 共 5 个状态,
        # goal 以 x=3 与截断值 x=4 两种赋值入队, 合计 <= 7
        self.assertLessEqual(result.explored_states, 7)

    def test_truncation_preserves_guard_semantics(self):
        # guard x>=3 在截断值 4(代表一切 >3)上判定与原值一致
        model = single_clock(
            Transition("exit", "s0", "goal", guard=(Constraint("x", ">=", 3),))
        )
        result = check_reachability(model, "goal", time_bound=1000)
        snapshots = replay(model, result.trace, time_bound=1000)
        self.assertEqual(snapshots[-1][1], "goal")


class TimeBoundTest(unittest.TestCase):
    """全局时间上界显式约束搜索。"""

    def _model(self):
        return single_clock(
            Transition("go", "s0", "goal", guard=(Constraint("x", ">=", 5),))
        )

    def test_within_bound(self):
        result = check_reachability(self._model(), "goal", time_bound=5)
        self.assertIs(result.verdict, Verdict.REACHABLE)

    def test_beyond_bound_unreachable(self):
        result = check_reachability(self._model(), "goal", time_bound=3)
        self.assertIs(result.verdict, Verdict.UNREACHABLE)

    def test_replay_rejects_overtime(self):
        with self.assertRaises(ReplayError):
            replay(self._model(), [Delay(4), Take("go")], time_bound=3)


class UndecidedTest(unittest.TestCase):
    """未搜索完就保持未决(UNKNOWN), 不误报。"""

    def test_budget_exhausted_gives_unknown(self):
        model = single_clock(
            Transition("go", "s0", "goal", guard=(Constraint("x", ">=", 5),))
        )
        result = check_reachability(model, "goal", time_bound=10, max_expansions=0)
        self.assertIs(result.verdict, Verdict.UNKNOWN)
        self.assertIsNone(result.trace)

    def test_sufficient_budget_still_decides(self):
        model = single_clock(
            Transition("go", "s0", "goal", guard=(Constraint("x", ">=", 5),))
        )
        result = check_reachability(model, "goal", time_bound=10, max_expansions=10_000)
        self.assertIs(result.verdict, Verdict.REACHABLE)


class ReplayTest(unittest.TestCase):
    """反例(可达轨迹)必须可重放, 篡改轨迹必须被拒绝。"""

    def test_example_trace_replays_to_target(self):
        model = build_model()
        result = check_reachability(model, "done", TIME_BOUND)
        self.assertIs(result.verdict, Verdict.REACHABLE)
        snapshots = replay(model, result.trace, TIME_BOUND)
        self.assertEqual(snapshots[-1][1], "done")

    def test_tampered_transition_name_rejected(self):
        model = build_model()
        with self.assertRaises(ReplayError):
            replay(model, [Take("nonexistent")], TIME_BOUND)

    def test_tampered_guard_rejected(self):
        model = build_model()
        # 未等 x>=2 就触发 heated
        with self.assertRaises(ReplayError):
            replay(model, [Take("power_on"), Delay(1), Take("heated")], TIME_BOUND)

    def test_wrong_source_location_rejected(self):
        model = build_model()
        with self.assertRaises(ReplayError):
            replay(model, [Take("heated")], TIME_BOUND)

    def test_format_trajectory_output(self):
        model = build_model()
        result = check_reachability(model, "done", TIME_BOUND)
        text = format_trajectory(model, result.trace, TIME_BOUND)
        self.assertIn("t=0", text)
        self.assertIn("@ done", text)
        self.assertIn("-- take serve -->", text)


class InvariantTest(unittest.TestCase):
    """位置 invariant 限制时间推进。"""

    def test_invariant_blocks_delay(self):
        model = single_clock(
            Transition("go", "s0", "goal", guard=(Constraint("x", ">=", 5),)),
            invariants={"s0": (Constraint("x", "<=", 2),)},
        )
        result = check_reachability(model, "goal", time_bound=10)
        self.assertIs(result.verdict, Verdict.UNREACHABLE)


if __name__ == "__main__":
    unittest.main()
