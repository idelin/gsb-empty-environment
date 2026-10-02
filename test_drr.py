"""Unittest suite for the (hierarchical) DRR schedulers.  All data synthetic."""
import json
import unittest

from drr_core import DRRScheduler, Tenant, Task
from hdr_core import HierDRR, Node, HTask
from starvation import check_starvation


def load_cfg(path):
    with open(path) as f:
        return json.load(f)


def mk(tid, w=1, q=2, cap=5, costs=()):
    t = Tenant(tid=tid, weight=w, quantum=q, cap=cap)
    for i, c in enumerate(costs):
        t.queue.append(Task(cost=c, tag=f"{tid}#{i}"))
    return t


class TestSingleLevel(unittest.TestCase):
    def test_acceptance_big_task_served_round3(self):
        # weight=1, quantum=2, head cost=5, cap=5 -> deficits 2,4,5 -> served in round 3
        s = DRRScheduler([mk("A", w=1, q=2, cap=5, costs=[5])])
        r1, r2, r3 = (s.run_round() for _ in range(3))
        self.assertEqual(r1["visits"][0]["deficit"], 2)
        self.assertEqual(r2["visits"][0]["deficit"], 4)
        served = r3["visits"][0]["served"]
        self.assertEqual(len(served), 1)
        self.assertEqual(served[0]["cost"], 5)
        self.assertEqual(served[0]["deficit_after_dequeue"], 0)
        self.assertEqual(r3["round"], 3)

    def test_acceptance_cap4_explicit_rejection(self):
        # cap=4 < cost 5: task can never be served -> explicit rejection evidence
        s = DRRScheduler([mk("A", w=1, q=2, cap=4, costs=[5, 2])])
        rec = s.run_round()
        served = rec["visits"][0]["served"]
        self.assertTrue(served[0]["rejected"])
        self.assertIn("can never be served", served[0]["reason"])
        self.assertEqual(len(s.tenants["A"].rejected), 1)
        # the feasible task behind it is still served in the same round
        self.assertEqual(served[1]["cost"], 2)

    def test_deficit_capped(self):
        s = DRRScheduler([mk("A", w=3, q=4, cap=5, costs=[5])])
        rec = s.run_round()
        self.assertEqual(rec["visits"][0]["deficit_after_credit"], 5)  # min(12, 5)

    def test_pause_resume(self):
        s = DRRScheduler([mk("A", costs=[1, 1]), mk("B", costs=[1])])
        s.pause("B")
        rec = s.run_round()
        vb = rec["visits"][1]
        self.assertTrue(vb["skipped"] and vb["paused"])
        self.assertEqual(s.tenants["B"].deficit, 0)  # no credit while paused
        s.resume("B")
        rec = s.run_round()
        self.assertNotIn("skipped", rec["visits"][1])
        self.assertEqual(rec["visits"][1]["served"][0]["cost"], 1)

    def test_dynamic_weight_update(self):
        s = DRRScheduler([mk("A", w=1, q=2, cap=10, costs=[9])])
        s.run_round()  # deficit 2
        s.set_weight("A", 3)
        rec = s.run_round()  # +6 -> 8
        self.assertEqual(rec["visits"][0]["credit"], 6)
        self.assertEqual(rec["visits"][0]["deficit"], 8)

    def test_head_of_line_blocking_no_skip(self):
        # small task behind a big head must NOT overtake (strict FIFO)
        s = DRRScheduler([mk("A", w=1, q=2, cap=6, costs=[5, 1])])
        rec = s.run_round()  # deficit 2, head 5 blocked, 1 must not pass
        self.assertEqual(rec["visits"][0]["served"], [])
        self.assertEqual(rec["visits"][0]["head_blocked"]["cost"], 5)


def build_tree():
    root = Node("G", weight=1, quantum=6, cap=12)
    t1 = Node("t1", weight=1, quantum=2, cap=5, parent=root)
    sub = Node("sub", weight=2, quantum=3, cap=8, parent=root)
    t2 = Node("t2", weight=1, quantum=2, cap=4, parent=sub)
    root.children = [t1, sub]
    sub.children = [t2]
    return root, t1, sub, t2


class TestHierarchical(unittest.TestCase):
    def test_path_constraint_and_simultaneous_deduction(self):
        root, t1, sub, t2 = build_tree()
        t1.tasks.append(HTask(cost=5, tag="t1#big"))
        s = HierDRR(root)
        rec = s.run_round()
        # t1 credited to 2 (<5) -> blocked; root credited to 6
        self.assertTrue(rec["dequeues"][0]["blocked"])
        s.run_round()  # t1 -> 4
        rec = s.run_round()  # t1 -> 5, root -> 12 (capped) >= 5 -> serve
        dq = [d for d in rec["dequeues"] if d.get("served")]
        self.assertEqual(len(dq), 1)
        self.assertEqual(dq[0]["path_deficits_after"], {"G": 7, "t1": 0})
        self.assertEqual(root.deficit, 7)
        self.assertEqual(t1.deficit, 0)

    def test_parent_deficit_limits_child(self):
        # parent cap smaller than child head cost -> explicit infeasible rejection
        root = Node("G", weight=1, quantum=1, cap=3)
        leaf = Node("a", weight=1, quantum=5, cap=10, parent=root)
        root.children = [leaf]
        leaf.tasks.append(HTask(cost=4, tag="a#x"))
        s = HierDRR(root)
        rec = s.run_round()
        self.assertEqual(rec["rejections"][0]["cost"], 4)
        self.assertIn("permanently infeasible", rec["rejections"][0]["reason"])

    def test_ancestor_pause_blocks_descendants(self):
        root, t1, sub, t2 = build_tree()
        t2.tasks.append(HTask(cost=1, tag="t2#0"))
        s = HierDRR(root)
        s.pause("sub")
        rec = s.run_round()
        self.assertEqual(t2.deficit, 0)
        self.assertTrue(any(x["node"] == "sub" and "paused" in x["reason"]
                            for x in rec["skipped"]))
        self.assertEqual(len(t2.tasks), 1)

    def test_parent_credited_once_per_round_anti_drain(self):
        root, t1, sub, t2 = build_tree()
        t1.tasks.append(HTask(cost=1, tag="t1#0"))
        t2.tasks.append(HTask(cost=1, tag="t2#0"))
        s = HierDRR(root)
        rec = s.run_round()
        root_credits = [c for c in rec["credits"] if c["node"] == "G"]
        self.assertEqual(len(root_credits), 1)  # two active children, one credit

    def test_idle_subtree_gets_no_credit(self):
        root, t1, sub, t2 = build_tree()
        t1.tasks.append(HTask(cost=1, tag="t1#0"))
        s = HierDRR(root)
        rec = s.run_round()
        credited = {c["node"] for c in rec["credits"]}
        self.assertNotIn("sub", credited)
        self.assertNotIn("t2", credited)

    def test_snapshot_restore_roundtrip(self):
        root, t1, sub, t2 = build_tree()
        t1.tasks.append(HTask(cost=5, tag="t1#big"))
        t2.tasks.append(HTask(cost=2, tag="t2#0"))
        s = HierDRR(root)
        s.run_round()
        snap = s.to_snapshot()
        json.dumps(snap)  # must be JSON-serialisable
        s2 = HierDRR.from_snapshot(json.loads(json.dumps(snap)))
        self.assertEqual(s2.to_snapshot(), snap)
        # identical continuations
        for _ in range(3):
            r1, r2 = s.run_round(), s2.run_round()
            self.assertEqual(r1, r2)

    def test_migration_truncates_deficit_no_free_service(self):
        root, t1, sub, t2 = build_tree()
        # give t1 a large accumulated deficit (cap 5)
        t1.deficit = 5
        # narrow new path: make a new parent with small cap
        narrow = Node("narrow", weight=1, quantum=1, cap=2, parent=root)
        root.children.append(narrow)
        s = HierDRR(root)
        ev = s.migrate("t1", "narrow")
        self.assertEqual(ev["deficit_before"], 5)
        self.assertEqual(ev["deficit_after"], 2)  # truncated to min path cap
        self.assertLessEqual(t1.deficit, 5)       # never increased
        self.assertIn(t1, narrow.children)
        self.assertNotIn(t1, root.children)  # detached from old parent
        # migration must never mint deficit
        t2.deficit = 1
        ev2 = s.migrate("t2", "narrow")
        self.assertEqual(ev2["deficit_after"], 1)  # kept, not topped up

    def test_events_via_config(self):
        cfg = load_cfg("examples/hier.json")
        s = HierDRR.from_config(cfg)
        out = s.run(cfg["rounds"], cfg["events"])
        kinds = [x["event"]["applied"]["op"] for x in out if "event" in x]
        self.assertEqual(kinds, ["pause", "resume", "migrate"])


class TestStarvationChecker(unittest.TestCase):
    def test_no_starvation_reported_with_bound(self):
        cfg = load_cfg("examples/check_ok.json")
        s = HierDRR.from_config(cfg)
        res = check_starvation(s, max_steps=5000)
        self.assertEqual(res["result"], "NO_STARVATION")
        self.assertEqual(res["starved_leaves"], [])
        self.assertGreater(res["state_space_bound"], res["states_explored"] - 1)
        self.assertGreater(res["cycle_length"], 0)

    def test_starvation_counterexample(self):
        cfg = load_cfg("examples/check_starve.json")
        s = HierDRR.from_config(cfg)
        res = check_starvation(s, max_steps=5000)
        self.assertEqual(res["result"], "STARVATION")
        self.assertEqual(res["starved_leaves"], ["b"])
        cx = res["counterexample"]
        self.assertGreater(cx["prefix_rounds"] + len(cx["cycle_trace"]), 0)
        # 'a' keeps being served inside the cycle, 'b' never is
        for rec in cx["cycle_trace"]:
            served = {d["leaf"] for d in rec["dequeues"] if d.get("served")}
            self.assertNotIn("b", served)

    def test_inconclusive_bound_respected(self):
        cfg = load_cfg("examples/check_ok.json")
        s = HierDRR.from_config(cfg)
        res = check_starvation(s, max_steps=1)
        self.assertEqual(res["result"], "INCONCLUSIVE")
        self.assertEqual(res["states_explored"], 1)


if __name__ == "__main__":
    unittest.main(verbosity=2)
