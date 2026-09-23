from __future__ import annotations

import json
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory

from mealy_planner.model import (
    apply_input,
    apply_reset,
    can_reset,
    initial_belief,
    is_goal,
    legal_inputs,
    load_machine,
)
from mealy_planner.planner import plan
from mealy_planner.verifier import VerificationError, verify

ROOT = Path(__file__).resolve().parents[1]
EXAMPLES = ROOT / "examples"


class PlannerExampleTests(unittest.TestCase):
    def test_common_coverage_requires_same_edge_everywhere(self) -> None:
        machine = load_machine(EXAMPLES / "same_output_no_reset.json")
        belief = initial_belief(machine)
        first = apply_input(machine, belief, "a")["0"]
        self.assertEqual(set(first.common), set())
        self.assertFalse(is_goal(machine, first))
        belief = apply_input(machine, first, "a")["0"]
        self.assertEqual(set(belief.common), {"s0:a", "s1:a"})
        self.assertFalse(is_goal(machine, belief))

    def test_same_output_strategy_optimal(self) -> None:
        result = plan(load_machine(EXAMPLES / "same_output_no_reset.json"), budget=10_000)
        self.assertEqual(result["status"], "solved")
        self.assertEqual(result["optimal_worst_case_cost"], 4)

    def test_partial_reset_strategy_optimal(self) -> None:
        result = plan(load_machine(EXAMPLES / "reset_partial_solvable.json"), budget=10_000)
        self.assertEqual(result["status"], "solved")
        self.assertEqual(result["optimal_worst_case_cost"], 7)

    def test_unsafe_indistinguishable_case_is_unsat(self) -> None:
        result = plan(load_machine(EXAMPLES / "unsafe_indistinguishable_unsat.json"), budget=10_000)
        self.assertEqual(result["status"], "unsat")
        self.assertTrue(result["diagnosis"]["losing_region_is_closed"])

    def test_sink_case_is_unsat(self) -> None:
        result = plan(load_machine(EXAMPLES / "sink_loss_unsat.json"), budget=10_000)
        self.assertEqual(result["status"], "unsat")
        self.assertTrue(result["diagnosis"]["losing_region_is_closed"])

    def test_budget_zero_is_undecided_not_unsat(self) -> None:
        result = plan(load_machine(EXAMPLES / "identify_then_act.json"), budget=0)
        self.assertEqual(result["status"], "undecided")
        self.assertEqual(result["lower_bound"], 6)
        self.assertIsNone(result["upper_bound"])
        self.assertTrue(result["search"]["truncated"])

    def test_reset_is_safe_only_when_every_world_is_resettable(self) -> None:
        machine = load_machine(EXAMPLES / "reset_partial_solvable.json")
        belief = initial_belief(machine)
        self.assertFalse(can_reset(machine, belief))
        self.assertIsNone(apply_reset(machine, belief))
        belief = apply_input(machine, belief, "p")["0"]
        self.assertTrue(can_reset(machine, belief))
        reset = apply_reset(machine, belief)
        self.assertIsNotNone(reset)
        self.assertEqual({world.state for world in reset.worlds}, {"s0"})

    def test_unsafe_input_omitted(self) -> None:
        machine = load_machine(EXAMPLES / "unsafe_indistinguishable_unsat.json")
        self.assertEqual(legal_inputs(machine, initial_belief(machine)), ("p",))


class VerifierTests(unittest.TestCase):
    def _result(self, name: str) -> dict:
        return plan(load_machine(EXAMPLES / f"{name}.json"), budget=10_000)

    def test_accepts_generated_solved_policies(self) -> None:
        with TemporaryDirectory() as tmp:
            for name, expected in [
                ("same_output_no_reset", 4),
                ("identify_then_act", 6),
                ("reset_partial_solvable", 7),
            ]:
                policy_path = Path(tmp) / f"{name}.json"
                policy_path.write_text(json.dumps(self._result(name)), encoding="utf-8")
                verdict = verify(EXAMPLES / f"{name}.json", policy_path)
                self.assertTrue(verdict["valid"])
                self.assertEqual(verdict["worst_case_cost"], expected)

    def test_rejects_unsafe_action(self) -> None:
        policy = {
            "schema_version": 1,
            "root": "n0",
            "nodes": [
                {"id": "n0", "type": "input", "input": "x",
                 "branches": [{"output": "0", "next": "n1"}]},
                {"id": "n1", "type": "terminal"},
            ],
        }
        with TemporaryDirectory() as tmp:
            policy_path = Path(tmp) / "bad.json"
            policy_path.write_text(json.dumps(policy), encoding="utf-8")
            with self.assertRaises(VerificationError):
                verify(EXAMPLES / "unsafe_indistinguishable_unsat.json", policy_path)

    def test_rejects_cycle(self) -> None:
        policy = {
            "schema_version": 1,
            "root": "n0",
            "nodes": [
                {"id": "n0", "type": "input", "input": "p",
                 "branches": [{"output": "0", "next": "n1"}, {"output": "1", "next": "n1"}]},
                {"id": "n1", "type": "reset", "next": "n0"},
            ],
        }
        with TemporaryDirectory() as tmp:
            policy_path = Path(tmp) / "cycle.json"
            policy_path.write_text(json.dumps(policy), encoding="utf-8")
            with self.assertRaises(VerificationError):
                verify(EXAMPLES / "reset_partial_solvable.json", policy_path)


if __name__ == "__main__":
    unittest.main()
