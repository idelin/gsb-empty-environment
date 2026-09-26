import json
import os
import subprocess
import sys
import tempfile
import unittest

from license_planner import (
    And,
    Leaf,
    Or,
    ParseError,
    options,
    parse_expression,
    solve,
    witness,
)


class TestParser(unittest.TestCase):
    def test_leaf(self):
        self.assertEqual(parse_expression("MIT"), Leaf("MIT"))

    def test_and_binds_tighter_than_or(self):
        tree = parse_expression("A OR B AND C")
        self.assertEqual(tree, Or(Leaf("A"), And(Leaf("B"), Leaf("C"))))

    def test_parentheses_override_precedence(self):
        tree = parse_expression("(A OR B) AND C")
        self.assertEqual(tree, And(Or(Leaf("A"), Leaf("B")), Leaf("C")))

    def test_keywords_are_case_insensitive(self):
        tree = parse_expression("a and b or c")
        self.assertEqual(tree, Or(And(Leaf("a"), Leaf("b")), Leaf("c")))

    def test_parse_errors(self):
        for bad in ("", "A AND", "AND A", "(A OR B", "A B", "A OR )"):
            with self.assertRaises(ParseError, msg=bad):
                parse_expression(bad)


class TestOptions(unittest.TestCase):
    def test_leaf(self):
        self.assertEqual(options(parse_expression("X")), [frozenset({"X"})])

    def test_or(self):
        got = options(parse_expression("X OR Y"))
        self.assertEqual(sorted(map(sorted, got)), [["X"], ["Y"]])

    def test_and(self):
        got = options(parse_expression("X AND Y"))
        self.assertEqual(sorted(map(sorted, got)), [["X", "Y"]])

    def test_nested_pruning(self):
        # (X AND Y) OR X  ->  {X} subsumes {X,Y}
        got = options(parse_expression("(X AND Y) OR X"))
        self.assertEqual(sorted(map(sorted, got)), [["X"]])


class TestSolveAcceptance(unittest.TestCase):
    def test_shared_license_counted_once(self):
        # A: X OR Y, B: Y; X costs 1, Y costs 2 -> pick Y, total 2 (not 4).
        result = solve(
            {"X": 1, "Y": 2},
            [],
            {"A": "X OR Y", "B": "Y"},
        )
        self.assertEqual(result["status"], "optimal")
        self.assertEqual(result["total_cost"], 2)
        self.assertEqual(result["selected"], ["Y"])

    def test_incompatible_pair_makes_problem_infeasible(self):
        # Y incompatible with Z; B: Y AND Z -> infeasible.
        result = solve(
            {"X": 1, "Y": 2, "Z": 3},
            [("Y", "Z")],
            {"A": "X OR Y", "B": "Y AND Z"},
        )
        self.assertEqual(result["status"], "infeasible")
        cert = result["certificate"]
        self.assertIn(["Y", "Z"], cert["incompatible_pairs"])
        self.assertEqual(cert["options_per_dependency"]["B"], [["Y", "Z"]])
        self.assertTrue(cert["blocking_combinations"])
        for entry in cert["blocking_combinations"]:
            self.assertIn(["Y", "Z"], entry["conflicts"])

    def test_incompatible_avoided_by_cheaper_compatible_choice(self):
        # Y-Z incompatible, so A must take X; B takes Y. Total 1 + 2 = 3.
        result = solve(
            {"X": 1, "Y": 2, "Z": 5},
            [("Y", "Z")],
            {"A": "X OR Z", "B": "Y"},
        )
        self.assertEqual(result["status"], "optimal")
        self.assertEqual(result["selected"], ["X", "Y"])
        self.assertEqual(result["total_cost"], 3)

    def test_duplicate_license_across_many_dependencies(self):
        result = solve(
            {"Y": 2},
            [],
            {"A": "Y", "B": "Y", "C": "Y AND Y"},
        )
        self.assertEqual(result["status"], "optimal")
        self.assertEqual(result["total_cost"], 2)
        self.assertEqual(result["selected"], ["Y"])

    def test_witness_structure(self):
        result = solve(
            {"X": 1, "Y": 2},
            [],
            {"A": "X OR Y", "B": "Y"},
        )
        self.assertEqual(result["witness"]["A"], {"or": {"license": "Y", "selected": True}})
        self.assertEqual(result["witness"]["B"], {"license": "Y", "selected": True})

    def test_witness_and_path(self):
        result = solve(
            {"X": 1, "Y": 2},
            [],
            {"A": "X AND Y"},
        )
        self.assertEqual(
            result["witness"]["A"],
            {"and": [{"license": "X", "selected": True},
                     {"license": "Y", "selected": True}]},
        )

    def test_witness_returns_none_when_unsatisfied(self):
        tree = parse_expression("X OR Y")
        self.assertIsNone(witness(tree, frozenset({"Z"})))

    def test_zero_dependencies(self):
        result = solve({"X": 1}, [], {})
        self.assertEqual(result["status"], "optimal")
        self.assertEqual(result["total_cost"], 0)
        self.assertEqual(result["selected"], [])

    def test_unknown_license_rejected(self):
        with self.assertRaises(ValueError):
            solve({"X": 1}, [], {"A": "X OR W"})
        with self.assertRaises(ValueError):
            solve({"X": 1}, [("X", "W")], {"A": "X"})

    def test_tie_broken_deterministically(self):
        result = solve(
            {"X": 1, "Y": 1},
            [],
            {"A": "X OR Y"},
        )
        self.assertEqual(result["total_cost"], 1)
        self.assertEqual(result["selected"], ["X"])  # lexicographic tie-break

    def test_parenthesized_expression(self):
        # (A OR B) AND (C OR D), cheapest combo A+C = 2.
        result = solve(
            {"A": 1, "B": 5, "C": 1, "D": 5},
            [],
            {"P": "(A OR B) AND (C OR D)"},
        )
        self.assertEqual(result["status"], "optimal")
        self.assertEqual(result["selected"], ["A", "C"])
        self.assertEqual(result["total_cost"], 2)


class TestCli(unittest.TestCase):
    def run_cli(self, problem):
        with tempfile.NamedTemporaryFile(
                "w", suffix=".json", delete=False) as fh:
            json.dump(problem, fh)
            path = fh.name
        try:
            proc = subprocess.run(
                [sys.executable, "license_planner.py", path],
                capture_output=True, text=True,
                cwd=os.path.dirname(os.path.abspath(__file__)),
            )
            return proc
        finally:
            os.unlink(path)

    def test_cli_optimal(self):
        proc = self.run_cli({
            "licenses": {"X": {"cost": 1}, "Y": {"cost": 2}},
            "incompatible": [],
            "dependencies": {"A": "X OR Y", "B": "Y"},
        })
        self.assertEqual(proc.returncode, 0, proc.stderr)
        out = json.loads(proc.stdout)
        self.assertEqual(out["status"], "optimal")
        self.assertEqual(out["total_cost"], 2)
        self.assertEqual(out["selected"], ["Y"])

    def test_cli_infeasible(self):
        proc = self.run_cli({
            "licenses": {"X": {"cost": 1}, "Y": {"cost": 2}, "Z": {"cost": 3}},
            "incompatible": [["Y", "Z"]],
            "dependencies": {"A": "X OR Y", "B": "Y AND Z"},
        })
        self.assertEqual(proc.returncode, 1, proc.stderr)
        out = json.loads(proc.stdout)
        self.assertEqual(out["status"], "infeasible")
        self.assertIn("certificate", out)

    def test_cli_usage_error(self):
        proc = subprocess.run(
            [sys.executable, "license_planner.py"],
            capture_output=True, text=True,
            cwd=os.path.dirname(os.path.abspath(__file__)),
        )
        self.assertEqual(proc.returncode, 2)


if __name__ == "__main__":
    unittest.main()
