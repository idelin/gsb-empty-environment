"""Unit tests for minwitness (Python 3.11, stdlib unittest)."""

from __future__ import annotations

import io
import subprocess
import sys
import unittest
from pathlib import Path

from minwitness import Circuit, CircuitError, run_script

MODULE = Path(__file__).with_name("minwitness.py")


def build_boundary_circuit() -> Circuit:
    """root = (a OR b) AND (b OR c), all facts asserted."""
    circuit = Circuit()
    for fact in ("a", "b", "c"):
        circuit.add_leaf(fact, asserted=True)
    circuit.add_gate("or", "left", ("a", "b"))
    circuit.add_gate("or", "right", ("b", "c"))
    circuit.add_gate("and", "root", ("left", "right"))
    circuit.add_root("root")
    return circuit


def greedy_local_witness(circuit: Circuit, node_id: str):
    """Per-subtree local optimum: AND unions child optima, OR picks the
    best child optimum. Documented counterexample strategy: it is NOT
    globally optimal when subtrees share leaves."""
    kind = circuit._kind[node_id]
    if kind == "leaf":
        return frozenset({node_id}) if circuit._asserted[node_id] else None
    child_sets = [greedy_local_witness(circuit, c)
                  for c in circuit._children[node_id]]
    if kind == "and":
        if any(s is None for s in child_sets):
            return None
        return frozenset().union(*child_sets)
    available = [s for s in child_sets if s is not None]
    if not available:
        return None
    return min(available, key=lambda s: (len(s), tuple(sorted(s))))


class AcceptanceBoundaryTest(unittest.TestCase):
    """root = (a OR b) AND (b OR c): global optimum is {b} alone."""

    def test_global_witness_is_b_only(self):
        circuit = build_boundary_circuit()
        self.assertTrue(circuit.value("root"))
        self.assertEqual(circuit.witness("root"), frozenset({"b"}))

    def test_greedy_local_is_not_global(self):
        # Greedy per-subtree picks {a} on the left and {b} on the right,
        # yielding the non-optimal two-fact set {a, b}.
        circuit = build_boundary_circuit()
        self.assertEqual(greedy_local_witness(circuit, "root"),
                         frozenset({"a", "b"}))
        self.assertLess(len(circuit.witness("root")), 2)

    def test_retract_b_forces_two_facts(self):
        circuit = build_boundary_circuit()
        circuit.retract_fact("b")
        self.assertTrue(circuit.value("root"))
        self.assertEqual(circuit.witness("root"), frozenset({"a", "c"}))

    def test_reassert_b_restores_single_fact(self):
        circuit = build_boundary_circuit()
        circuit.retract_fact("b")
        circuit.assert_fact("b")
        self.assertEqual(circuit.witness("root"), frozenset({"b"}))

    def test_retract_all_makes_root_false(self):
        circuit = build_boundary_circuit()
        for fact in ("a", "b", "c"):
            circuit.retract_fact(fact)
        self.assertFalse(circuit.value("root"))
        self.assertIsNone(circuit.witness("root"))


class WitnessSemanticsTest(unittest.TestCase):
    def test_duplicate_leaf_counted_once(self):
        # root = (a OR b) AND a  ->  {a} suffices, counted once.
        circuit = Circuit()
        circuit.add_leaf("a", asserted=True)
        circuit.add_leaf("b", asserted=True)
        circuit.add_gate("or", "ab", ("a", "b"))
        circuit.add_gate("and", "root", ("ab", "a"))
        self.assertEqual(circuit.witness("root"), frozenset({"a"}))

    def test_shared_gate_subgraph_counted_once(self):
        # root = g AND g where g = a AND b  ->  {a, b}, not {a, b, a, b}.
        circuit = Circuit()
        circuit.add_leaf("a", asserted=True)
        circuit.add_leaf("b", asserted=True)
        circuit.add_gate("and", "g", ("a", "b"))
        circuit.add_gate("and", "root", ("g", "g"))
        self.assertEqual(circuit.witness("root"), frozenset({"a", "b"}))

    def test_size_beats_lexicographic(self):
        # root = (a AND b) OR z  ->  {z} wins on size.
        circuit = Circuit()
        for fact in ("a", "b", "z"):
            circuit.add_leaf(fact, asserted=True)
        circuit.add_gate("and", "ab", ("a", "b"))
        circuit.add_gate("or", "root", ("ab", "z"))
        self.assertEqual(circuit.witness("root"), frozenset({"z"}))

    def test_lexicographic_tiebreak(self):
        # root = (x AND y) OR (a AND z)  ->  both size 2, {a, z} wins.
        circuit = Circuit()
        for fact in ("a", "x", "y", "z"):
            circuit.add_leaf(fact, asserted=True)
        circuit.add_gate("and", "xy", ("x", "y"))
        circuit.add_gate("and", "az", ("a", "z"))
        circuit.add_gate("or", "root", ("xy", "az"))
        self.assertEqual(circuit.witness("root"), frozenset({"a", "z"}))

    def test_incremental_matches_fresh_recompute(self):
        # After a retract, incrementally maintained values/witnesses must
        # equal a from-scratch build of the same final state.
        circuit = build_boundary_circuit()
        circuit.retract_fact("b")
        fresh = Circuit()
        for fact, state in (("a", True), ("b", False), ("c", True)):
            fresh.add_leaf(fact, asserted=state)
        fresh.add_gate("or", "left", ("a", "b"))
        fresh.add_gate("or", "right", ("b", "c"))
        fresh.add_gate("and", "root", ("left", "right"))
        for node in ("a", "b", "c", "left", "right", "root"):
            self.assertEqual(circuit.value(node), fresh.value(node), node)
            self.assertEqual(circuit.witness(node), fresh.witness(node), node)

    def test_errors(self):
        circuit = Circuit()
        circuit.add_leaf("a")
        with self.assertRaises(CircuitError):
            circuit.add_leaf("a")
        with self.assertRaises(CircuitError):
            circuit.add_gate("and", "g", ("missing",))
        with self.assertRaises(CircuitError):
            circuit.value("missing")
        circuit.add_gate("or", "g", ("a",))
        with self.assertRaises(CircuitError):
            circuit.assert_fact("g")


class CliTest(unittest.TestCase):
    def run_cli(self, script_text: str) -> str:
        result = subprocess.run(
            [sys.executable, str(MODULE)],
            input=script_text, capture_output=True, text=True, timeout=30,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        return result.stdout

    def test_boundary_script(self):
        output = self.run_cli(
            "leaf a asserted\n"
            "leaf b asserted\n"
            "leaf c asserted\n"
            "or left a b\n"
            "or right b c\n"
            "and root left right\n"
            "root root\n"
            "witness\n"
            "retract b\n"
            "witness\n"
            "assert b\n"
            "witness\n"
        )
        self.assertEqual(output.splitlines(), [
            "root true b",
            "root true a c",
            "root true b",
        ])

    def test_value_and_false_witness(self):
        output = self.run_cli(
            "leaf a asserted\n"
            "leaf b\n"
            "and root a b\n"
            "root root\n"
            "value root\n"
            "witness\n"
            "assert b\n"
            "witness\n"
        )
        self.assertEqual(output.splitlines(), [
            "root false",
            "root false",
            "root true a b",
        ])

    def test_run_script_writes_to_stream(self):
        circuit = Circuit()
        out = io.StringIO()
        run_script(["leaf a asserted", "root a", "witness"], circuit, out)
        self.assertEqual(out.getvalue(), "a true a\n")

    def test_cli_error_exit_code(self):
        result = subprocess.run(
            [sys.executable, str(MODULE)],
            input="and g missing\n", capture_output=True, text=True, timeout=30,
        )
        self.assertEqual(result.returncode, 2)
        self.assertIn("error:", result.stderr)


if __name__ == "__main__":
    unittest.main()
