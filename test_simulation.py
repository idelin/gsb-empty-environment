"""unittest 测试套件：python3 -m unittest -v"""
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from machine import Machine, MachineError
from simulation import (
    MISSING_INPUT,
    UNEXPECTED_OUTPUT,
    ReplayError,
    Step,
    check_simulation,
    greatest_simulation,
)


def make_spec():
    """带循环的请求/响应协议规范。"""
    return Machine(
        name="spec",
        states={"idle", "busy", "ready"},
        initial="idle",
        inputs={"req", "cancel"},
        outputs={"ack", "data"},
        transitions=[
            ("idle", "req", "busy"),
            ("busy", "ack", "ready"),
            ("busy", "cancel", "idle"),
            ("ready", "data", "idle"),
        ],
    )


class VarianceTest(unittest.TestCase):
    """输入/输出分支方差：增加输入与增加输出的兼容性不同。"""

    def setUp(self):
        self.spec = make_spec()

    def test_extra_input_is_compatible(self):
        candidate = Machine(
            name="extra-input",
            states={"idle", "busy", "ready"},
            initial="idle",
            inputs={"req", "cancel", "reset"},
            outputs={"ack"},
            transitions=[
                ("idle", "req", "busy"),
                ("idle", "reset", "idle"),
                ("busy", "ack", "ready"),
                ("busy", "cancel", "idle"),
            ],
        )
        result = check_simulation(candidate, self.spec)
        self.assertTrue(result.ok)
        self.assertIsNone(result.counterexample)

    def test_extra_output_is_incompatible(self):
        candidate = Machine(
            name="extra-output",
            states={"idle", "busy", "ready"},
            initial="idle",
            inputs={"req", "cancel"},
            outputs={"ack", "data", "error"},
            transitions=[
                ("idle", "req", "busy"),
                ("busy", "ack", "ready"),
                ("busy", "cancel", "idle"),
                ("busy", "error", "idle"),
                ("ready", "data", "idle"),
            ],
        )
        result = check_simulation(candidate, self.spec)
        self.assertFalse(result.ok)
        cex = result.counterexample
        self.assertEqual(cex.failure.kind, UNEXPECTED_OUTPUT)
        self.assertEqual(cex.failure.action, "error")
        self.assertEqual([st.action for st in cex.steps], ["req"])
        self.assertTrue(cex.replay(candidate, self.spec))

    def test_missing_required_input_is_incompatible(self):
        candidate = Machine(
            name="missing-input",
            states={"idle"},
            initial="idle",
            inputs={"cancel"},
            outputs=set(),
            transitions=[],
        )
        result = check_simulation(candidate, self.spec)
        self.assertFalse(result.ok)
        cex = result.counterexample
        self.assertEqual(cex.failure.kind, MISSING_INPUT)
        self.assertEqual(cex.failure.action, "req")
        self.assertEqual(cex.steps, [])
        self.assertTrue(cex.replay(candidate, self.spec))


class LoopFixpointTest(unittest.TestCase):
    """循环协议：不动点迭代必须终止并给出正确结论。"""

    def test_self_loop_protocol_terminates(self):
        spec = Machine(
            name="loop-spec",
            states={"q"},
            initial="q",
            inputs={"a"},
            outputs={"b"},
            transitions=[("q", "a", "q"), ("q", "b", "q")],
        )
        candidate = Machine(
            name="loop-candidate",
            states={"p"},
            initial="p",
            inputs={"a"},
            outputs={"b"},
            transitions=[("p", "a", "p"), ("p", "b", "p")],
        )
        relation, iterations = greatest_simulation(candidate, spec)
        self.assertIn(("p", "q"), relation)
        self.assertGreaterEqual(iterations, 1)
        self.assertTrue(check_simulation(candidate, spec).ok)

    def test_loop_candidate_missing_input_fails(self):
        spec = Machine(
            name="loop-spec",
            states={"q"},
            initial="q",
            inputs={"a"},
            outputs={"b"},
            transitions=[("q", "a", "q"), ("q", "b", "q")],
        )
        candidate = Machine(
            name="loop-bad",
            states={"p"},
            initial="p",
            inputs=set(),
            outputs={"b"},
            transitions=[("p", "b", "p")],
        )
        result = check_simulation(candidate, spec)
        self.assertFalse(result.ok)
        self.assertEqual(result.counterexample.failure.kind, MISSING_INPUT)
        self.assertEqual(result.counterexample.failure.action, "a")

    def test_deep_loop_counterexample_reaches_cycle(self):
        spec = Machine(
            name="cycle-spec",
            states={"s0", "s1"},
            initial="s0",
            inputs={"go"},
            outputs={"tick", "done"},
            transitions=[
                ("s0", "go", "s1"),
                ("s1", "tick", "s1"),
                ("s1", "done", "s0"),
            ],
        )
        candidate = Machine(
            name="cycle-bad",
            states={"c0", "c1"},
            initial="c0",
            inputs={"go"},
            outputs={"tick", "done", "bang"},
            transitions=[
                ("c0", "go", "c1"),
                ("c1", "tick", "c1"),
                ("c1", "done", "c0"),
                ("c1", "bang", "c1"),
            ],
        )
        result = check_simulation(candidate, spec)
        self.assertFalse(result.ok)
        cex = result.counterexample
        self.assertEqual(cex.failure.kind, UNEXPECTED_OUTPUT)
        self.assertEqual(cex.failure.action, "bang")
        self.assertEqual([st.action for st in cex.steps], ["go"])
        self.assertTrue(cex.replay(candidate, spec))


class ReplayTest(unittest.TestCase):
    """反例可逐步重放，篡改后的路径必须被拒绝。"""

    def setUp(self):
        self.spec = make_spec()
        self.candidate = Machine(
            name="bad",
            states={"idle", "busy", "ready"},
            initial="idle",
            inputs={"req", "cancel"},
            outputs={"ack", "data", "error"},
            transitions=[
                ("idle", "req", "busy"),
                ("busy", "ack", "ready"),
                ("busy", "cancel", "idle"),
                ("busy", "error", "idle"),
                ("ready", "data", "idle"),
            ],
        )
        self.cex = check_simulation(self.candidate, self.spec).counterexample

    def test_replay_reports_each_step(self):
        seen = []
        self.assertTrue(
            self.cex.replay(self.candidate, self.spec,
                            on_step=lambda i, st: seen.append((i, st.action)))
        )
        self.assertEqual(seen, [(1, "req")])

    def test_tampered_step_is_rejected(self):
        bad_step = Step("cancel", "input", "idle", "busy", "idle", "busy")
        tampered = type(self.cex)(steps=[bad_step], failure=self.cex.failure)
        with self.assertRaises(ReplayError):
            tampered.replay(self.candidate, self.spec)

    def test_tampered_failure_is_rejected(self):
        good_failure = self.cex.failure
        fake = type(good_failure)(MISSING_INPUT, "cancel",
                                  good_failure.candidate_state,
                                  good_failure.spec_state)
        tampered = type(self.cex)(steps=list(self.cex.steps), failure=fake)
        with self.assertRaises(ReplayError):
            tampered.replay(self.candidate, self.spec)

    def test_exported_script_runs_standalone(self):
        script = self.cex.to_script(self.candidate, self.spec)
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "cex_replay.py"
            path.write_text(script, encoding="utf-8")
            proc = subprocess.run(
                [sys.executable, str(path)],
                capture_output=True, text=True,
            )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn("反例重放成功", proc.stdout)


class NondeterminismTest(unittest.TestCase):
    """非确定性规范：候选必须覆盖规范的每一个输入分支。"""

    def test_partial_branch_coverage_fails(self):
        spec = Machine(
            name="nd-spec",
            states={"s0", "s1", "s2"},
            initial="s0",
            inputs={"a"},
            outputs={"x", "y"},
            transitions=[
                ("s0", "a", "s1"),
                ("s0", "a", "s2"),
                ("s1", "x", "s1"),
                ("s2", "y", "s2"),
            ],
        )
        candidate = Machine(
            name="nd-candidate",
            states={"c0", "c1"},
            initial="c0",
            inputs={"a"},
            outputs={"x"},
            transitions=[
                ("c0", "a", "c1"),
                ("c1", "x", "c1"),
            ],
        )
        result = check_simulation(candidate, spec)
        self.assertFalse(result.ok)
        cex = result.counterexample
        self.assertEqual([st.action for st in cex.steps], ["a"])
        self.assertEqual(cex.steps[0].spec_dst, "s2")
        self.assertEqual(cex.failure.kind, UNEXPECTED_OUTPUT)
        self.assertEqual(cex.failure.action, "x")
        self.assertTrue(cex.replay(candidate, spec))


class MachineValidationTest(unittest.TestCase):
    def test_alphabet_must_be_disjoint(self):
        with self.assertRaises(MachineError):
            Machine("m", {"s"}, "s", {"a"}, {"a"}, [])

    def test_transition_must_use_declared_alphabet(self):
        with self.assertRaises(MachineError):
            Machine("m", {"s"}, "s", {"a"}, set(), [("s", "b", "s")])

    def test_transition_must_use_declared_states(self):
        with self.assertRaises(MachineError):
            Machine("m", {"s"}, "s", {"a"}, set(), [("s", "a", "t")])

    def test_roundtrip_dict(self):
        spec = make_spec()
        clone = Machine.from_dict(spec.to_dict())
        self.assertEqual(clone.states, spec.states)
        self.assertEqual(clone.inputs, spec.inputs)
        self.assertEqual(clone.outputs, spec.outputs)
        self.assertEqual(clone.to_dict(), spec.to_dict())


if __name__ == "__main__":
    unittest.main()
