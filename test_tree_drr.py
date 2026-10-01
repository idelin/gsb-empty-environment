"""Unittest suite for the hierarchical (tenant-tree) DRR scheduler."""
import unittest

from tree_drr import InfeasibleTaskError, TreeDRR


def build_fixture():
    """Synthetic two-level tenant tree:
    root
    +-- g1 (w1 q4 cap8)
    |   +-- A (w1 q3 cap6): a1/2, a2/5
    |   +-- B (w1 q3 cap6): b1/6
    +-- g2 (w1 q4 cap8)
        +-- C (w1 q3 cap6): c1/1
    """
    t = TreeDRR()
    t.add_node("g1", "root", weight=1, quantum=4, cap=8)
    t.add_node("g2", "root", weight=1, quantum=4, cap=8)
    t.add_node("A", "g1", weight=1, quantum=3, cap=6, leaf=True)
    t.add_node("B", "g1", weight=1, quantum=3, cap=6, leaf=True)
    t.add_node("C", "g2", weight=1, quantum=3, cap=6, leaf=True)
    t.enqueue("A", "a1", 2)
    t.enqueue("A", "a2", 5)
    t.enqueue("B", "b1", 6)
    t.enqueue("C", "c1", 1)
    return t


class PathDeficitTest(unittest.TestCase):
    def test_dequeue_path_evidence_and_simultaneous_deduction(self):
        t = build_fixture()
        events = t.run_round()
        deq = [e for e in events if e["event"] == "dequeue"]
        self.assertEqual(len(deq), 2)  # a1 and c1
        a1 = next(e for e in deq if e["task"] == "a1")
        self.assertEqual(a1["path"], ["g1", "A"])
        self.assertEqual(a1["deficit_before"], {"g1": 4, "A": 3})
        self.assertEqual(a1["deficit_after"], {"g1": 2, "A": 1})
        # simultaneous deduction: parent g1 was charged together with leaf A
        self.assertEqual(t.nodes["g1"].deficit, 2)
        self.assertEqual(t.nodes["A"].deficit, 1)
        c1 = next(e for e in deq if e["task"] == "c1")
        self.assertEqual(c1["path"], ["g2", "C"])
        self.assertEqual(c1["deficit_after"], {"g2": 3, "C": 2})

    def test_head_blocked_names_limiting_node(self):
        t = build_fixture()
        events = t.run_round()
        blocked = [e for e in events if e["event"] == "head_blocked"]
        a2 = next(e for e in blocked if e["head_task"] == "a2")
        # first limiting node on path [g1, A]: g1 deficit 2 < cost 5
        self.assertEqual(a2["blocker"], "g1")
        self.assertEqual(a2["path_deficits"], {"g1": 2, "A": 1})
        b1 = next(e for e in blocked if e["head_task"] == "b1")
        # g1 was drained to 2 by a1's dequeue, so g1 blocks first
        self.assertEqual(b1["blocker"], "g1")
        self.assertEqual(b1["path_deficits"], {"g1": 2, "B": 3})

    def test_path_cap_rejects_infeasible_task(self):
        t = build_fixture()
        ev = t.try_enqueue("A", "huge", 7)  # path cap = min(8, 6) = 6
        self.assertEqual(ev["event"], "reject")
        self.assertEqual(ev["reason"], "cost_exceeds_path_cap")
        self.assertEqual(ev["path_cap"], 6)
        with self.assertRaises(InfeasibleTaskError):
            t.enqueue("A", "huge", 7)

    def test_big_feasible_task_eventually_served(self):
        t = build_fixture()
        served = {}
        for _ in range(10):
            for e in t.run_round():
                if e["event"] == "dequeue":
                    served[e["task"]] = t.round
            if "a2" in served and "b1" in served:
                break
        self.assertEqual(served["b1"], 2)  # cost 6 feasible at cap 6
        self.assertEqual(served["a2"], 4)  # waits for g1 deficit to reach 5+


class PauseAndRoundRuleTest(unittest.TestCase):
    def test_ancestor_pause_freezes_descendants(self):
        t = build_fixture()
        t.run_round()
        t.pause("g1")
        events = t.run_round()
        credited = {e["node"] for e in events if e["event"] == "credit"}
        self.assertNotIn("g1", credited)
        self.assertNotIn("A", credited)
        self.assertNotIn("B", credited)
        self.assertIn({"event": "skip", "round": 2, "node": "g1", "reason": "paused"}, events)
        self.assertFalse(any(e["event"] == "dequeue" and e["leaf"] in ("A", "B") for e in events))
        # deficits frozen while paused
        self.assertEqual(t.nodes["A"].deficit, 1)
        t.resume("g1")
        events = t.run_round()
        credited = {e["node"] for e in events if e["event"] == "credit"}
        self.assertIn("g1", credited)

    def test_idle_child_not_credited_and_reset(self):
        t = build_fixture()
        t.run_round()  # c1 served; C queue now empty
        events = t.run_round()
        # g2 subtree idle: no credit for g2 or C, and g2 deficit reset to 0
        credited = {e["node"] for e in events if e["event"] == "credit"}
        self.assertNotIn("g2", credited)
        self.assertNotIn("C", credited)
        resets = [e for e in events if e["event"] == "reset"]
        self.assertTrue(any(e["node"] == "g2" and e["deficit_before"] == 3 for e in resets))
        self.assertEqual(t.nodes["g2"].deficit, 0)

    def test_credit_once_per_round_per_node(self):
        t = build_fixture()
        events = t.run_round()
        credits = [e["node"] for e in events if e["event"] == "credit"]
        self.assertEqual(len(credits), len(set(credits)))


class SnapshotRestoreTest(unittest.TestCase):
    def test_restore_continues_identically(self):
        t = build_fixture()
        t.run_round()
        t.run_round()
        snap = t.snapshot()
        events_cont = t.run_round() + t.run_round()

        t2 = TreeDRR.restore(snap)
        self.assertEqual(t2.round, snap["round"])
        events_replayed = t2.run_round() + t2.run_round()
        self.assertEqual(events_cont, events_replayed)
        self.assertEqual(t.snapshot(), t2.snapshot())

    def test_snapshot_is_json_serializable(self):
        import json
        t = build_fixture()
        t.run_round()
        json.dumps(t.snapshot())


class MigrationTest(unittest.TestCase):
    def build_migration_fixture(self):
        t = TreeDRR()
        t.add_node("g1", "root", weight=1, quantum=4, cap=8)
        t.add_node("g3", "root", weight=1, quantum=2, cap=4)
        t.add_node("A", "g1", weight=1, quantum=3, cap=10, leaf=True)
        t.enqueue("A", "big", 8)
        return t

    def test_migration_truncates_deficit_to_new_path_cap(self):
        t = self.build_migration_fixture()
        t.run_round()  # A deficit 3
        t.run_round()  # A deficit 6 (big/8 still blocked)
        self.assertEqual(t.nodes["A"].deficit, 6)
        events = t.migrate_leaf("A", "g3")
        ev = events[0]
        self.assertEqual(ev["event"], "migrate")
        self.assertEqual(ev["deficit_before"], 6)
        self.assertEqual(ev["path_cap"], 4)  # min(g3 cap 4, A cap 10)
        self.assertEqual(ev["deficit_after"], 4)
        self.assertLessEqual(ev["deficit_after"], ev["deficit_before"])  # no minting
        self.assertEqual(t.nodes["A"].deficit, 4)
        self.assertEqual(t.nodes["A"].parent.node_id, "g3")

    def test_migration_drops_newly_infeasible_tasks(self):
        t = self.build_migration_fixture()
        t.run_round()
        events = t.migrate_leaf("A", "g3")  # new path cap 4 < cost 8
        ev = events[0]
        self.assertEqual(ev["dropped_tasks"], [{"task": "big", "cost": 8}])
        self.assertEqual(len(t.nodes["A"].queue), 0)

    def test_migration_never_increases_deficit(self):
        t = self.build_migration_fixture()
        t.run_round()
        before = t.nodes["A"].deficit
        t.migrate_leaf("A", "g3")
        self.assertLessEqual(t.nodes["A"].deficit, before)
        t.migrate_leaf("A", "g1")  # move back to a fatter path
        self.assertLessEqual(t.nodes["A"].deficit, before)

    def test_migrated_leaf_schedulable_under_new_parent(self):
        t = self.build_migration_fixture()
        t.migrate_leaf("A", "g3")
        t.enqueue("A", "small", 4)  # feasible under new path cap 4
        events = t.run_until_idle()
        deq = [e for e in events if e["event"] == "dequeue" and e["task"] == "small"]
        self.assertEqual(len(deq), 1)
        self.assertEqual(deq[0]["path"], ["g3", "A"])


if __name__ == "__main__":
    unittest.main()
