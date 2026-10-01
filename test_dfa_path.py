"""Unittest suite for dfa_path (finite-alphabet path compliance)."""

import json
import os
import subprocess
import sys
import tempfile
import unittest

from dfa_path import SpecError, solve

HERE = os.path.dirname(os.path.abspath(__file__))
CLI = os.path.join(HERE, "dfa_path.py")


def acceptance_boundary_spec():
    """Graph has a direct s->t edge 'a' (cost 1) but the DFA only accepts 'ab'.

    The only legal walk is s -> m -> t with labels a,b and total cost 2.
    """
    return {
        "alphabet": ["a", "b"],
        "graph": {
            "vertices": ["s", "m", "t"],
            "edges": [
                {"id": 0, "src": "s", "dst": "t", "label": "a", "cost": 1},
                {"id": 1, "src": "s", "dst": "m", "label": "a", "cost": 1},
                {"id": 2, "src": "m", "dst": "t", "label": "b", "cost": 1},
            ],
        },
        "dfa": {
            "states": ["q0", "q1", "q2"],
            "start": "q0",
            "accept": ["q2"],
            "transitions": {"q0": {"a": "q1"}, "q1": {"b": "q2"}},
        },
        "source": "s",
        "target": "t",
    }


class AcceptanceBoundaryTest(unittest.TestCase):
    def test_direct_edge_not_returned(self):
        result = solve(acceptance_boundary_spec())
        self.assertTrue(result["found"])
        self.assertEqual(result["cost"], 2)
        self.assertEqual(result["edge_count"], 2)
        self.assertEqual(result["labels"], "ab")
        self.assertEqual(result["edge_ids"], [1, 2])
        self.assertEqual(result["vertex_path"], ["s", "m", "t"])
        self.assertEqual(result["dfa_state_path"], ["q0", "q1", "q2"])

    def test_dual_evidence_consistent(self):
        spec = acceptance_boundary_spec()
        result = solve(spec)
        trans = {
            (st, ch): nxt
            for st, row in spec["dfa"]["transitions"].items()
            for ch, nxt in row.items()
        }
        # DFA state path must follow the label string from the start state.
        self.assertEqual(result["dfa_state_path"][0], spec["dfa"]["start"])
        for ch, q_from, q_to in zip(
            result["labels"], result["dfa_state_path"], result["dfa_state_path"][1:]
        ):
            self.assertEqual(trans[(q_from, ch)], q_to)
        self.assertIn(result["dfa_state_path"][-1], spec["dfa"]["accept"])
        # Vertex path must start/end at source/target and use declared edges.
        edges = {e["id"]: e for e in spec["graph"]["edges"]}
        self.assertEqual(result["vertex_path"][0], spec["source"])
        self.assertEqual(result["vertex_path"][-1], spec["target"])
        for i, eid in enumerate(result["edge_ids"]):
            e = edges[eid]
            self.assertEqual(e["src"], result["vertex_path"][i])
            self.assertEqual(e["dst"], result["vertex_path"][i + 1])
            self.assertEqual(e["label"], result["labels"][i])


class ZeroCostCycleTest(unittest.TestCase):
    def test_zero_cost_cycle_terminates(self):
        spec = {
            "alphabet": ["a", "b"],
            "graph": {
                "vertices": ["s", "u", "t"],
                "edges": [
                    {"id": 0, "src": "s", "dst": "u", "label": "a", "cost": 0},
                    {"id": 1, "src": "u", "dst": "u", "label": "a", "cost": 0},  # zero loop
                    {"id": 2, "src": "u", "dst": "t", "label": "b", "cost": 0},
                    {"id": 3, "src": "s", "dst": "t", "label": "b", "cost": 5},
                ],
            },
            "dfa": {
                "states": ["q0", "q1"],
                "start": "q0",
                "accept": ["q1"],
                "transitions": {"q0": {"a": "q0", "b": "q1"}, "q1": {"b": "q1"}},
            },
            "source": "s",
            "target": "t",
        }
        result = solve(spec)
        self.assertTrue(result["found"])
        self.assertEqual(result["cost"], 0)
        # Fewest edges among cost-0 walks: a,b via u (2 edges), not the loop.
        self.assertEqual(result["edge_count"], 2)
        self.assertEqual(result["labels"], "ab")
        self.assertEqual(result["vertex_path"], ["s", "u", "t"])

    def test_zero_cost_cycle_forced_loop(self):
        # DFA accepts only "aab": the zero-cost loop must be traversed once.
        spec = {
            "alphabet": ["a", "b"],
            "graph": {
                "vertices": ["s", "u", "t"],
                "edges": [
                    {"id": 0, "src": "s", "dst": "u", "label": "a", "cost": 0},
                    {"id": 1, "src": "u", "dst": "u", "label": "a", "cost": 0},
                    {"id": 2, "src": "u", "dst": "t", "label": "b", "cost": 0},
                ],
            },
            "dfa": {
                "states": ["q0", "q1", "q2", "q3"],
                "start": "q0",
                "accept": ["q3"],
                "transitions": {
                    "q0": {"a": "q1"},
                    "q1": {"a": "q2"},
                    "q2": {"b": "q3"},
                },
            },
            "source": "s",
            "target": "t",
        }
        result = solve(spec)
        self.assertTrue(result["found"])
        self.assertEqual(result["labels"], "aab")
        self.assertEqual(result["vertex_path"], ["s", "u", "u", "t"])
        self.assertEqual(result["dfa_state_path"], ["q0", "q1", "q2", "q3"])


class TieBreakTest(unittest.TestCase):
    def test_fewest_edges_beats_more_edges(self):
        spec = {
            "alphabet": ["a"],
            "graph": {
                "vertices": ["s", "m", "t"],
                "edges": [
                    {"id": 0, "src": "s", "dst": "t", "label": "a", "cost": 2},
                    {"id": 1, "src": "s", "dst": "m", "label": "a", "cost": 1},
                    {"id": 2, "src": "m", "dst": "t", "label": "a", "cost": 1},
                ],
            },
            "dfa": {
                "states": ["q0", "q1"],
                "start": "q0",
                "accept": ["q1"],
                "transitions": {"q0": {"a": "q1"}, "q1": {"a": "q1"}},
            },
            "source": "s",
            "target": "t",
        }
        result = solve(spec)
        self.assertEqual(result["cost"], 2)
        self.assertEqual(result["edge_count"], 1)
        self.assertEqual(result["edge_ids"], [0])

    def test_lexicographic_labels(self):
        # Both walks: cost 2, 2 edges; "ab" beats "ba".
        spec = {
            "alphabet": ["a", "b"],
            "graph": {
                "vertices": ["s", "x", "y", "t"],
                "edges": [
                    {"id": 0, "src": "s", "dst": "x", "label": "b", "cost": 1},
                    {"id": 1, "src": "x", "dst": "t", "label": "a", "cost": 1},
                    {"id": 2, "src": "s", "dst": "y", "label": "a", "cost": 1},
                    {"id": 3, "src": "y", "dst": "t", "label": "b", "cost": 1},
                ],
            },
            "dfa": {
                "states": ["q0", "q1", "q2"],
                "start": "q0",
                "accept": ["q2"],
                "transitions": {
                    "q0": {"a": "q1", "b": "q1"},
                    "q1": {"a": "q2", "b": "q2"},
                },
            },
            "source": "s",
            "target": "t",
        }
        result = solve(spec)
        self.assertEqual(result["labels"], "ab")
        self.assertEqual(result["edge_ids"], [2, 3])

    def test_lexicographic_edge_ids(self):
        # Parallel edges with identical label/cost: smaller id sequence wins.
        spec = {
            "alphabet": ["a"],
            "graph": {
                "vertices": ["s", "t"],
                "edges": [
                    {"id": 7, "src": "s", "dst": "t", "label": "a", "cost": 1},
                    {"id": 3, "src": "s", "dst": "t", "label": "a", "cost": 1},
                ],
            },
            "dfa": {
                "states": ["q0", "q1"],
                "start": "q0",
                "accept": ["q1"],
                "transitions": {"q0": {"a": "q1"}},
            },
            "source": "s",
            "target": "t",
        }
        result = solve(spec)
        self.assertEqual(result["edge_ids"], [3])


class StructuralTest(unittest.TestCase):
    def test_no_path(self):
        spec = {
            "alphabet": ["a"],
            "graph": {
                "vertices": ["s", "t"],
                "edges": [{"id": 0, "src": "s", "dst": "s", "label": "a", "cost": 1}],
            },
            "dfa": {
                "states": ["q0", "q1"],
                "start": "q0",
                "accept": ["q1"],
                "transitions": {"q0": {"a": "q1"}},
            },
            "source": "s",
            "target": "t",
        }
        self.assertEqual(solve(spec), {"found": False})

    def test_empty_walk_when_source_equals_target_and_start_accepts(self):
        spec = {
            "alphabet": ["a"],
            "graph": {"vertices": ["s"], "edges": []},
            "dfa": {
                "states": ["q0"],
                "start": "q0",
                "accept": ["q0"],
                "transitions": {},
            },
            "source": "s",
            "target": "s",
        }
        result = solve(spec)
        self.assertTrue(result["found"])
        self.assertEqual(result["cost"], 0)
        self.assertEqual(result["edge_count"], 0)
        self.assertEqual(result["labels"], "")
        self.assertEqual(result["vertex_path"], ["s"])
        self.assertEqual(result["dfa_state_path"], ["q0"])

    def test_source_equals_target_requires_loop(self):
        spec = {
            "alphabet": ["a"],
            "graph": {
                "vertices": ["s"],
                "edges": [{"id": 0, "src": "s", "dst": "s", "label": "a", "cost": 3}],
            },
            "dfa": {
                "states": ["q0", "q1"],
                "start": "q0",
                "accept": ["q1"],
                "transitions": {"q0": {"a": "q1"}},
            },
            "source": "s",
            "target": "s",
        }
        result = solve(spec)
        self.assertTrue(result["found"])
        self.assertEqual(result["cost"], 3)
        self.assertEqual(result["labels"], "a")
        self.assertEqual(result["vertex_path"], ["s", "s"])

    def test_repeated_vertices_allowed(self):
        # DFA accepts "aa"; graph forces revisiting u.
        spec = {
            "alphabet": ["a"],
            "graph": {
                "vertices": ["s", "u", "t"],
                "edges": [
                    {"id": 0, "src": "s", "dst": "u", "label": "a", "cost": 1},
                    {"id": 1, "src": "u", "dst": "u", "label": "a", "cost": 1},
                    {"id": 2, "src": "u", "dst": "t", "label": "a", "cost": 1},
                ],
            },
            "dfa": {
                "states": ["q0", "q1", "q2"],
                "start": "q0",
                "accept": ["q2"],
                "transitions": {"q0": {"a": "q1"}, "q1": {"a": "q2"}},
            },
            "source": "s",
            "target": "t",
        }
        result = solve(spec)
        self.assertEqual(result["labels"], "aa")
        self.assertEqual(result["edge_ids"], [0, 2])


class ValidationTest(unittest.TestCase):
    def base(self):
        return acceptance_boundary_spec()

    def test_negative_cost_rejected(self):
        spec = self.base()
        spec["graph"]["edges"][0]["cost"] = -1
        with self.assertRaises(SpecError):
            solve(spec)

    def test_bool_cost_rejected(self):
        spec = self.base()
        spec["graph"]["edges"][0]["cost"] = True
        with self.assertRaises(SpecError):
            solve(spec)

    def test_label_outside_alphabet_rejected(self):
        spec = self.base()
        spec["graph"]["edges"][0]["label"] = "z"
        with self.assertRaises(SpecError):
            solve(spec)

    def test_duplicate_edge_id_rejected(self):
        spec = self.base()
        spec["graph"]["edges"][1]["id"] = 0
        with self.assertRaises(SpecError):
            solve(spec)

    def test_mixed_edge_id_types_rejected(self):
        spec = self.base()
        spec["graph"]["edges"][1]["id"] = "1"
        with self.assertRaises(SpecError):
            solve(spec)

    def test_unknown_vertex_rejected(self):
        spec = self.base()
        spec["graph"]["edges"][0]["dst"] = "nowhere"
        with self.assertRaises(SpecError):
            solve(spec)

    def test_unknown_dfa_state_rejected(self):
        spec = self.base()
        spec["dfa"]["transitions"]["q0"]["b"] = "qx"
        with self.assertRaises(SpecError):
            solve(spec)

    def test_float_costs(self):
        spec = self.base()
        for e in spec["graph"]["edges"]:
            e["cost"] = 0.5
        result = solve(spec)
        self.assertEqual(result["cost"], 1)


class CliTest(unittest.TestCase):
    def run_cli(self, args, stdin_text=None):
        return subprocess.run(
            [sys.executable, CLI, *args],
            input=stdin_text,
            capture_output=True,
            text=True,
            timeout=30,
        )

    def test_cli_stdin(self):
        proc = self.run_cli([], json.dumps(acceptance_boundary_spec()))
        self.assertEqual(proc.returncode, 0, proc.stderr)
        result = json.loads(proc.stdout)
        self.assertEqual(result["cost"], 2)
        self.assertEqual(result["labels"], "ab")

    def test_cli_file(self):
        with tempfile.NamedTemporaryFile(
            "w", suffix=".json", delete=False, encoding="utf-8"
        ) as fh:
            json.dump(acceptance_boundary_spec(), fh)
            path = fh.name
        try:
            proc = self.run_cli([path])
        finally:
            os.unlink(path)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(json.loads(proc.stdout)["edge_ids"], [1, 2])

    def test_cli_invalid_json(self):
        proc = self.run_cli([], "{not json")
        self.assertEqual(proc.returncode, 2)
        self.assertIn("invalid JSON", proc.stderr)

    def test_cli_invalid_spec(self):
        spec = acceptance_boundary_spec()
        spec["graph"]["edges"][0]["cost"] = -5
        proc = self.run_cli([], json.dumps(spec))
        self.assertEqual(proc.returncode, 2)
        self.assertIn("invalid spec", proc.stderr)


if __name__ == "__main__":
    unittest.main()
