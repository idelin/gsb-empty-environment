import json
import subprocess
import sys
import unittest
from pathlib import Path

from release_select import exhaustive_verify, parse_instance, solve
from release_select.core import InstanceError, _is_feasible

REPO_ROOT = Path(__file__).resolve().parent.parent


def make_instance(capacity, changes, mutex_groups=()):
    return parse_instance({
        "capacity": capacity,
        "changes": changes,
        "mutex_groups": [list(g) for g in mutex_groups],
    })


class AcceptanceBoundaryTest(unittest.TestCase):
    """A(benefit 5) depends on B(benefit -2); capacity allows both.
    The optimum must be {A, B} with benefit 3, never A alone with 5."""

    def setUp(self):
        self.instance = make_instance(10, [
            {"id": "A", "benefit": 5, "cost": 2, "depends_on": ["B"]},
            {"id": "B", "benefit": -2, "cost": 1},
        ])

    def test_optimum_is_bundle_ab_with_benefit_3(self):
        sol = solve(self.instance)
        self.assertEqual(sol.selected_ids, ("A", "B"))
        self.assertEqual(sol.total_benefit, 3)
        self.assertEqual(sol.total_cost, 3)

    def test_a_alone_is_infeasible(self):
        self.assertFalse(_is_feasible(frozenset({"A"}), self.instance))

    def test_dependency_evidence_present_and_satisfied(self):
        sol = solve(self.instance)
        dep = [e for e in sol.evidence if e["constraint"] == "dependency"]
        self.assertEqual(len(dep), 1)
        self.assertEqual(dep[0]["change"], "A")
        self.assertEqual(dep[0]["requires"], "B")
        self.assertTrue(dep[0]["satisfied"])
        cap = [e for e in sol.evidence if e["constraint"] == "capacity"]
        self.assertEqual(cap[0]["used"], 3)
        self.assertEqual(cap[0]["limit"], 10)
        self.assertTrue(cap[0]["satisfied"])

    def test_verifier_proves_optimal(self):
        sol = solve(self.instance)
        report = exhaustive_verify(self.instance, sol)
        self.assertTrue(report.proved_optimal)
        self.assertTrue(report.matches_solution)
        self.assertEqual(report.best_benefit, 3)
        self.assertEqual(report.best_ids, ("A", "B"))
        self.assertEqual(report.subsets_enumerated, 4)
        self.assertEqual(report.feasible_subsets, 3)  # {}, {B}, {A,B}


class DependencyCycleTest(unittest.TestCase):
    """Dependency cycles are atomic units: taken whole or not at all."""

    def test_cycle_taken_as_whole_when_net_positive(self):
        inst = make_instance(10, [
            {"id": "X", "benefit": 4, "cost": 1, "depends_on": ["Y"]},
            {"id": "Y", "benefit": -1, "cost": 1, "depends_on": ["X"]},
        ])
        sol = solve(inst)
        self.assertEqual(sol.selected_ids, ("X", "Y"))
        self.assertEqual(sol.total_benefit, 3)
        cyc = [e for e in sol.evidence
               if e["constraint"] == "dependency_cycle_group"]
        self.assertEqual(len(cyc), 1)
        self.assertEqual(cyc[0]["members"], ["X", "Y"])
        self.assertTrue(cyc[0]["satisfied"])

    def test_cycle_dropped_when_net_negative(self):
        inst = make_instance(10, [
            {"id": "X", "benefit": 1, "cost": 1, "depends_on": ["Y"]},
            {"id": "Y", "benefit": -5, "cost": 1, "depends_on": ["X"]},
        ])
        sol = solve(inst)
        self.assertEqual(sol.selected_ids, ())
        self.assertEqual(sol.total_benefit, 0)

    def test_cycle_with_external_dependency(self):
        inst = make_instance(10, [
            {"id": "X", "benefit": 4, "cost": 1, "depends_on": ["Y"]},
            {"id": "Y", "benefit": 1, "cost": 1, "depends_on": ["X", "Z"]},
            {"id": "Z", "benefit": -2, "cost": 1},
        ])
        sol = solve(inst)
        self.assertEqual(sol.selected_ids, ("X", "Y", "Z"))
        self.assertEqual(sol.total_benefit, 3)
        report = exhaustive_verify(inst, sol)
        self.assertTrue(report.proved_optimal)


class MutexTest(unittest.TestCase):
    def test_mutex_picks_higher_benefit(self):
        inst = make_instance(10, [
            {"id": "P", "benefit": 5, "cost": 1},
            {"id": "Q", "benefit": 4, "cost": 1},
        ], mutex_groups=[("P", "Q")])
        sol = solve(inst)
        self.assertEqual(sol.selected_ids, ("P",))
        self.assertEqual(sol.total_benefit, 5)
        mutex = [e for e in sol.evidence if e["constraint"] == "mutex"]
        self.assertEqual(mutex[0]["selected_in_group"], ["P"])
        self.assertTrue(mutex[0]["satisfied"])

    def test_mutex_with_three_members(self):
        inst = make_instance(10, [
            {"id": "P", "benefit": 3, "cost": 1},
            {"id": "Q", "benefit": 4, "cost": 1},
            {"id": "R", "benefit": 5, "cost": 1},
        ], mutex_groups=[("P", "Q", "R")])
        sol = solve(inst)
        self.assertEqual(sol.selected_ids, ("R",))


class CapacityTest(unittest.TestCase):
    def test_capacity_excludes_overfull_combination(self):
        inst = make_instance(3, [
            {"id": "A", "benefit": 5, "cost": 2},
            {"id": "B", "benefit": 4, "cost": 2},
        ])
        sol = solve(inst)
        self.assertEqual(sol.selected_ids, ("A",))
        self.assertEqual(sol.total_benefit, 5)

    def test_zero_capacity_selects_nothing(self):
        inst = make_instance(0, [
            {"id": "A", "benefit": 5, "cost": 1},
        ])
        sol = solve(inst)
        self.assertEqual(sol.selected_ids, ())

    def test_bundle_blocked_by_capacity_falls_back(self):
        inst = make_instance(2, [
            {"id": "A", "benefit": 5, "cost": 2, "depends_on": ["B"]},
            {"id": "B", "benefit": -2, "cost": 1},
            {"id": "C", "benefit": 2, "cost": 1},
        ])
        sol = solve(inst)
        # {A,B} costs 3 > capacity 2, so best feasible is {C}.
        self.assertEqual(sol.selected_ids, ("C",))
        self.assertEqual(sol.total_benefit, 2)
        report = exhaustive_verify(inst, sol)
        self.assertTrue(report.proved_optimal)


class TieBreakTest(unittest.TestCase):
    def test_fewest_changes_wins(self):
        # Capacity 2 forces a choice: {S} (benefit 6, 1 change) vs
        # {T1, T2} (benefit 6, 2 changes); fewer changes wins.
        inst = make_instance(2, [
            {"id": "S", "benefit": 6, "cost": 2},
            {"id": "T1", "benefit": 3, "cost": 1},
            {"id": "T2", "benefit": 3, "cost": 1},
        ])
        sol = solve(inst)
        self.assertEqual(sol.selected_ids, ("S",))
        self.assertEqual(sol.total_benefit, 6)

    def test_lexicographic_ids_win(self):
        inst = make_instance(10, [
            {"id": "beta", "benefit": 3, "cost": 1},
            {"id": "alpha", "benefit": 3, "cost": 1},
        ], mutex_groups=[("alpha", "beta")])
        sol = solve(inst)
        self.assertEqual(sol.selected_ids, ("alpha",))

    def test_empty_selection_when_all_negative(self):
        inst = make_instance(10, [
            {"id": "A", "benefit": -1, "cost": 1},
            {"id": "B", "benefit": -2, "cost": 1},
        ])
        sol = solve(inst)
        self.assertEqual(sol.selected_ids, ())
        self.assertEqual(sol.total_benefit, 0)


class VerifierTest(unittest.TestCase):
    def test_verifier_flags_mismatched_solution(self):
        inst = make_instance(10, [
            {"id": "A", "benefit": 5, "cost": 1},
            {"id": "B", "benefit": 4, "cost": 1},
        ])
        good = solve(inst)
        bad = type(good)(selected_ids=("B",), total_benefit=4,
                         total_cost=1, evidence=())
        report = exhaustive_verify(inst, bad)
        self.assertFalse(report.matches_solution)
        self.assertFalse(report.proved_optimal)

    def test_verifier_agrees_on_mixed_example(self):
        data = json.loads(
            (REPO_ROOT / "examples" / "mixed_constraints.json").read_text())
        inst = parse_instance(data)
        sol = solve(inst)
        report = exhaustive_verify(inst, sol)
        self.assertTrue(report.proved_optimal)
        # Three subsets reach benefit 10 under capacity 8:
        #   {cache-core, cache-tune, log-prune, log-rotate} (4 changes)
        #   {cache-core, cache-tune, index-v2}              (3 changes)
        #   {index-v2, log-prune, log-rotate}               (3 changes)
        # Fewest-changes then lexicographic tie-break picks the second.
        self.assertEqual(sol.total_benefit, 10)
        self.assertEqual(sol.selected_ids,
                         ("cache-core", "cache-tune", "index-v2"))
        self.assertEqual(sol.total_cost, 8)


class ParseValidationTest(unittest.TestCase):
    def test_rejects_unknown_dependency(self):
        with self.assertRaises(InstanceError):
            make_instance(1, [{"id": "A", "benefit": 1, "cost": 1,
                               "depends_on": ["nope"]}])

    def test_rejects_duplicate_ids(self):
        with self.assertRaises(InstanceError):
            make_instance(1, [{"id": "A", "benefit": 1, "cost": 1},
                              {"id": "A", "benefit": 2, "cost": 1}])

    def test_rejects_negative_capacity(self):
        with self.assertRaises(InstanceError):
            make_instance(-1, [])

    def test_rejects_negative_cost(self):
        with self.assertRaises(InstanceError):
            make_instance(1, [{"id": "A", "benefit": 1, "cost": -1}])

    def test_rejects_unknown_mutex_member(self):
        with self.assertRaises(InstanceError):
            make_instance(1, [{"id": "A", "benefit": 1, "cost": 1}],
                          mutex_groups=[("A", "ghost")])

    def test_rejects_non_integer_benefit(self):
        with self.assertRaises(InstanceError):
            make_instance(1, [{"id": "A", "benefit": 1.5, "cost": 1}])


class CliTest(unittest.TestCase):
    def run_cli(self, *args, stdin=None):
        return subprocess.run(
            [sys.executable, "-m", "release_select", *args],
            cwd=REPO_ROOT, input=stdin, capture_output=True, text=True)

    def test_cli_acceptance_boundary_file(self):
        path = str(REPO_ROOT / "examples" / "acceptance_boundary.json")
        proc = self.run_cli(path)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        report = json.loads(proc.stdout)
        self.assertEqual(report["status"], "optimal")
        self.assertEqual(report["selected_ids"], ["A", "B"])
        self.assertEqual(report["total_benefit"], 3)
        self.assertTrue(report["verification"]["proved_optimal"])
        self.assertTrue(any(e["constraint"] == "capacity"
                            for e in report["evidence"]))
        self.assertTrue(any(e["constraint"] == "dependency"
                            for e in report["evidence"]))

    def test_cli_stdin(self):
        payload = json.dumps({
            "capacity": 1,
            "changes": [{"id": "A", "benefit": 7, "cost": 1}],
        })
        proc = self.run_cli("-", stdin=payload)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        report = json.loads(proc.stdout)
        self.assertEqual(report["selected_ids"], ["A"])
        self.assertEqual(report["total_benefit"], 7)

    def test_cli_no_verify_marks_unverified(self):
        path = str(REPO_ROOT / "examples" / "acceptance_boundary.json")
        proc = self.run_cli(path, "--no-verify")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        report = json.loads(proc.stdout)
        self.assertEqual(report["status"], "optimal-unverified")
        self.assertIsNone(report["verification"])

    def test_cli_invalid_json_exit_code(self):
        proc = self.run_cli("-", stdin="{not json")
        self.assertEqual(proc.returncode, 2)
        self.assertIn("invalid JSON", proc.stderr)

    def test_cli_invalid_instance_exit_code(self):
        proc = self.run_cli("-", stdin='{"capacity": -1, "changes": []}')
        self.assertEqual(proc.returncode, 2)
        self.assertIn("invalid instance", proc.stderr)

    def test_cli_missing_file_exit_code(self):
        proc = self.run_cli("/nonexistent/path.json")
        self.assertEqual(proc.returncode, 2)


if __name__ == "__main__":
    unittest.main()
