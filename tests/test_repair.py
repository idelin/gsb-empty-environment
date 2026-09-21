import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from license_solver import (
    INFEASIBLE,
    OPTIMAL,
    Solver,
    load_build,
    load_repository,
    repair,
)

SAMPLES = Path(__file__).resolve().parent.parent / "samples"


class RepairTest(unittest.TestCase):
    def setUp(self):
        self.build = load_build(SAMPLES / "build_app.json")
        base = Solver(load_repository(SAMPLES / "repo_app_v1.json"), self.build).solve()
        assert base.status == OPTIMAL
        self.assertEqual(base.cost, (0, 0))
        self.previous = {
            n: (str(s.version), s.branches) for n, s in base.selection.items()
        }

    def test_repair_releases_only_the_conflicting_pin(self):
        repo_v2 = load_repository(SAMPLES / "repo_app_v2.json")
        result = repair(repo_v2, self.build, self.previous, {"shared": "1.1.0"})
        # The pinned plugin@1.0.0 (Apache-2.0) clashes with the forced
        # GPL-2.0-only shared@1.1.0, so local repair must fail and the core
        # must name exactly that pin; the utils pin is irrelevant.
        self.assertEqual(result.strategy, "full")
        self.assertIn("forced shared@1.1.0", result.conflict_core)
        self.assertIn("pin plugin@1.0.0", result.conflict_core)
        self.assertFalse(any("utils" in c for c in result.conflict_core))
        self.assertEqual(str(result.selection["plugin"].version), "1.1.0")
        self.assertEqual(str(result.selection["netlib"].version), "1.1.0")
        self.assertEqual(str(result.selection["shared"].version), "1.1.0")
        # GPL-2.0-only strong copyleft propagates up the static edges.
        self.assertEqual(result.cost, (4, 4))
        self.assertEqual(str(result.selection["utils"].version), "1.0.0")

    def test_unrepairable_change_reports_core_and_infeasible(self):
        repo_v3 = load_repository(SAMPLES / "repo_app_v3.json")
        result = repair(repo_v3, self.build, self.previous, {"shared": "1.1.0"})
        self.assertEqual(result.status, INFEASIBLE)
        self.assertEqual(result.strategy, "none")
        self.assertIsNone(result.selection)
        # No carried-over pin is responsible: the forced change itself is
        # infeasible, so the core shrinks to just the forced change.
        self.assertEqual(result.conflict_core, ["forced shared@1.1.0"])

    def test_noop_change_keeps_previous_selection(self):
        repo_v1 = load_repository(SAMPLES / "repo_app_v1.json")
        result = repair(repo_v1, self.build, self.previous, {"shared": "1.0.0"})
        self.assertEqual(result.strategy, "local")
        self.assertEqual(result.cost, (0, 0))
        self.assertEqual(result.conflict_core, [])


if __name__ == "__main__":
    unittest.main()
