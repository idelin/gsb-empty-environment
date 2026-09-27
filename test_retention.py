"""Unittest suite for the hierarchical retention deletion planner.

All data is synthetic.  Run with: python3.11 -m unittest -v
"""
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent
sys.path.insert(0, str(ROOT))

from retention import INFINITE, RetentionStore, run_scenario  # noqa: E402


class AcceptanceBoundaryTests(unittest.TestCase):
    """The three acceptance boundaries from the specification."""

    def setUp(self):
        self.store = RetentionStore()
        self.store.mkdir("/p20", retention_until=20)
        self.store.mkdir("/p5", retention_until=5)
        self.store.create("/p20/report.txt", retention_until=10)

    def test_parent20_file10_time15_not_deletable(self):
        self.store.advance(15)
        self.assertEqual(self.store.due(), [])
        node = self.store.resolve("/p20/report.txt")
        self.assertEqual(self.store.deletable_at(node), 20)  # max(20, 10)

    def test_move_to_bound5_dir_time15_deletable(self):
        self.store.move("/p20/report.txt", "/p5")
        node = self.store.resolve("/p5/report.txt")
        self.assertEqual(self.store.deletable_at(node), 10)  # max(5, 10)
        self.store.advance(15)
        due = self.store.due()
        self.assertEqual([d["path"] for d in due], ["/p5/report.txt"])

    def test_any_legal_hold_blocks_deletion(self):
        self.store.set_hold("/p20/report.txt", True)
        self.store.advance(1_000_000)
        self.assertEqual(self.store.due(), [])
        plan = self.store.plan()
        self.assertEqual(plan["entries"][0]["earliest"], INFINITE)
        results = self.store.execute(plan, now=1_000_000)
        self.assertEqual(results[0]["status"], "skipped")
        self.assertIn("legal hold", results[0]["reason"])

    def test_directory_legal_hold_blocks_descendants(self):
        self.store.set_hold("/p20", True)  # hold on the directory
        self.store.advance(1_000_000)
        self.assertEqual(self.store.due(), [])
        self.store.set_hold("/p20", False)  # release -> deletable again
        self.assertEqual(len(self.store.due()), 1)


class HierarchyTests(unittest.TestCase):
    def test_directory_move_recomputes_children(self):
        store = RetentionStore()
        store.mkdir("/a")  # no retention of its own
        store.create("/a/f", retention_until=1)
        self.assertEqual(store.deletable_at(store.resolve("/a/f")), 1)
        store.mkdir("/b", retention_until=20)
        store.move("/a", "/b")  # whole subtree inherits the new bound
        self.assertEqual(store.deletable_at(store.resolve("/b/a/f")), 20)

    def test_nested_max_of_all_bounds(self):
        store = RetentionStore()
        store.mkdir("/a", retention_until=3)
        store.mkdir("/a/b", retention_until=8)
        store.create("/a/b/f", retention_until=5)
        self.assertEqual(store.deletable_at(store.resolve("/a/b/f")), 8)

    def test_move_rejects_own_subtree(self):
        store = RetentionStore()
        store.mkdir("/a")
        store.mkdir("/a/b")
        with self.assertRaises(ValueError):
            store.move("/a", "/a/b")


class VersionSnapshotTests(unittest.TestCase):
    def setUp(self):
        self.store = RetentionStore()
        self.store.mkdir("/a", retention_until=1)
        self.store.create("/a/f", retention_until=1)
        self.store.advance(5)

    def test_version_change_skips_with_reason(self):
        plan = self.store.plan()
        self.store.set_retention("/a/f", 100)  # mutate after the snapshot
        results = self.store.execute(plan, now=5)
        self.assertEqual(results[0]["status"], "skipped")
        self.assertIn("version changed", results[0]["reason"])
        self.assertIn("v1", results[0]["reason"])
        self.assertIn("v2", results[0]["reason"])
        self.assertEqual(self.store.path_of(self.store.resolve("/a/f")), "/a/f")

    def test_move_after_snapshot_invalidates_plan(self):
        self.store.mkdir("/b")
        plan = self.store.plan()
        self.store.move("/a/f", "/b")
        results = self.store.execute(plan, now=5)
        self.assertEqual(results[0]["status"], "skipped")
        self.assertIn("version changed", results[0]["reason"])

    def test_deleted_node_skipped_with_reason(self):
        plan = self.store.plan()
        self.store.delete("/a/f")
        results = self.store.execute(plan, now=5)
        self.assertEqual(results[0]["status"], "skipped")
        self.assertIn("no longer exists", results[0]["reason"])

    def test_unmodified_plan_executes(self):
        plan = self.store.plan()
        results = self.store.execute(plan, now=5)
        self.assertEqual(results[0]["status"], "deleted")
        with self.assertRaises(KeyError):
            self.store.resolve("/a/f")

    def test_not_yet_due_skipped_with_reason(self):
        plan = self.store.plan()
        results = self.store.execute(plan, now=0)  # before the bound of 1
        self.assertEqual(results[0]["status"], "skipped")
        self.assertIn("not yet due", results[0]["reason"])


class IndexTests(unittest.TestCase):
    def test_time_advance_pops_only_expired_entries(self):
        store = RetentionStore()
        store.mkdir("/d")
        for bound in (5, 10, 15, 20):
            store.create(f"/d/f{bound}", retention_until=bound)
        store.advance(12)
        due = store.due()
        self.assertEqual([d["earliest"] for d in due], [5, 10])
        self.assertEqual(store.heap_pops, 2)  # no full scan of the 4 files
        store.advance(25)
        due = store.due()
        self.assertEqual([d["earliest"] for d in due], [15, 20])
        self.assertEqual(store.heap_pops, 4)

    def test_stale_index_entries_discarded_lazily(self):
        store = RetentionStore()
        store.mkdir("/d")
        store.create("/d/f", retention_until=10)
        store.set_retention("/d/f", 50)  # old heap entry (bound 10) is stale
        store.advance(15)
        self.assertEqual(store.due(), [])
        self.assertEqual(store.heap_pops, 1)  # stale entry popped, discarded
        store.advance(60)
        self.assertEqual(len(store.due()), 1)

    def test_hold_removal_reindexes(self):
        store = RetentionStore()
        store.mkdir("/d")
        store.create("/d/f", retention_until=10, legal_hold=True)
        store.advance(100)
        self.assertEqual(store.due(), [])
        store.set_hold("/d/f", False)
        self.assertEqual(len(store.due()), 1)


class CliTests(unittest.TestCase):
    def test_acceptance_scenario_end_to_end(self):
        scenario = {
            "ops": [
                {"op": "mkdir", "path": "/p20", "retention_until": 20},
                {"op": "mkdir", "path": "/p5", "retention_until": 5},
                {"op": "create", "path": "/p20/report.txt",
                 "retention_until": 10},
                {"op": "tick", "now": 15},
                {"op": "due"},
                {"op": "plan", "id": "before-move"},
                {"op": "execute", "plan": "before-move"},
                {"op": "move", "src": "/p20/report.txt", "dst_dir": "/p5"},
                {"op": "due"},
                {"op": "plan", "id": "after-move"},
                {"op": "execute", "plan": "after-move"},
            ]
        }
        with tempfile.NamedTemporaryFile(
                "w", suffix=".json", delete=False) as handle:
            json.dump(scenario, handle)
            scenario_path = handle.name
        proc = subprocess.run(
            [sys.executable, str(ROOT / "retention.py"), "run", scenario_path],
            capture_output=True, text=True)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        results = json.loads(proc.stdout)["results"]
        by_op = [r for r in results if r["op"] == "due"]
        self.assertEqual(by_op[0]["due"], [])            # t=15 under p20
        self.assertEqual(len(by_op[1]["due"]), 1)         # t=15 under p5
        executions = [r for r in results if r["op"] == "execute"]
        self.assertEqual(executions[0]["results"][0]["status"], "skipped")
        self.assertIn("not yet due",
                      executions[0]["results"][0]["reason"])
        self.assertEqual(executions[1]["results"][0]["status"], "deleted")

    def test_run_scenario_legal_hold(self):
        results = run_scenario([
            {"op": "mkdir", "path": "/d", "retention_until": 1},
            {"op": "create", "path": "/d/f", "retention_until": 1,
             "legal_hold": True},
            {"op": "tick", "now": 999},
            {"op": "due"},
            {"op": "plan", "id": "p"},
            {"op": "execute", "plan": "p"},
        ])
        self.assertEqual(results[3]["due"], [])
        self.assertEqual(results[4]["plan"]["entries"][0]["earliest"],
                         INFINITE)
        self.assertEqual(results[5]["results"][0]["status"], "skipped")
        self.assertIn("legal hold", results[5]["results"][0]["reason"])


if __name__ == "__main__":
    unittest.main()
