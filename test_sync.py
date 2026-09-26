#!/usr/bin/env python3
"""sync.py 的 unittest 测试。所有数据均为合成数据。"""

import json
import os
import random
import subprocess
import sys
import tempfile
import unittest

import sync
from sync import Event, SyncNode, causal_orders, check_convergence


def ev(id, op, obj, clock, replica, deps=(), name=None, parent=None):
    return Event(id=id, op=op, obj=obj, clock=clock, replica=replica,
                 deps=frozenset(deps), name=name, parent=parent)


def deliver_all(events, order=None):
    node = SyncNode()
    seq = order if order is not None else [e.id for e in events]
    by_id = {e.id: e for e in events}
    for eid in seq:
        node.deliver(by_id[eid])
    return node


def random_causal_orders(events, n, seed=42):
    """随机采样 n 个因果合法投递顺序。"""
    rng = random.Random(seed)
    by_id = {e.id: e for e in events}
    orders = []
    for _ in range(n):
        done, remaining, order = set(), set(by_id), []
        while remaining:
            ready = [i for i in remaining if by_id[i].deps <= done]
            pick = rng.choice(ready)
            remaining.discard(pick)
            done.add(pick)
            order.append(pick)
        orders.append(order)
    return orders


class AcceptanceTests(unittest.TestCase):
    """验收边界：并发同名仲裁 & 移入后代拒绝。"""

    def setUp(self):
        # a 与 b 并发改名为同名 "same.txt"（同一目录 root）
        self.conflict_events = [
            ev("e1", "create", "a", 1, "R1", name="a.txt", parent="root"),
            ev("e2", "create", "b", 1, "R2", name="b.txt", parent="root"),
            ev("e3", "rename", "a", 2, "R1", deps=["e1"], name="same.txt"),
            ev("e4", "rename", "b", 2, "R2", deps=["e2"], name="same.txt"),
        ]

    def test_concurrent_same_name_all_causal_orders_same_winner(self):
        """对象 a 与 b 并发改为同名：所有投递顺序下同一高戳对象胜。"""
        events = self.conflict_events
        orders = list(causal_orders(events))
        self.assertGreater(len(orders), 1)
        results = set()
        for order in orders:
            state = deliver_all(events, order).materialize()
            # 高戳 (2,"R2") > (2,"R1")，故 b 恒胜
            self.assertEqual(state.paths["b"], "/same.txt")
            self.assertEqual(state.paths["a"], "/.conflicts/same.txt")
            self.assertEqual(len(state.conflicts), 1)
            self.assertEqual(state.conflicts[0]["obj"], "a")
            self.assertEqual(state.conflicts[0]["winner"], "b")
            self.assertIn("name-conflict", state.conflicts[0]["reason"])
            results.add(state.canonical())
        self.assertEqual(len(results), 1, "不同投递顺序产生了不同结果")

    def test_converge_command_agrees(self):
        result = check_convergence(self.conflict_events)
        self.assertEqual(result["status"], "converged")
        self.assertEqual(result["orders_tested"], 6)

    def test_move_into_own_descendant_rejected(self):
        """a 移到其后代目录必须被拒绝，而不是形成环。"""
        events = [
            ev("e1", "create", "a", 1, "R1", name="a", parent="root"),
            ev("e2", "create", "b", 2, "R1", deps=["e1"], name="b", parent="a"),
            ev("e3", "move", "a", 3, "R1", deps=["e2"], parent="b"),
        ]
        state = deliver_all(events).materialize()
        # 环 a -> b -> a 被打破：造成环的移动 e3 被拒绝
        self.assertEqual(state.paths["a"], "/a")
        self.assertEqual(state.paths["b"], "/a/b")
        self.assertEqual(len(state.rejected), 1)
        self.assertEqual(state.rejected[0]["event"], "e3")
        self.assertIn("cycle", state.rejected[0]["reason"])
        # 无环
        self.assertIsNone(sync._find_cycle(state.parents))

    def test_longer_cycle_broken_deterministically(self):
        """三节点环 a->b->c->a：拒绝环上最高戳事件，且与投递顺序无关。"""
        events = [
            ev("e1", "create", "a", 1, "R1", name="a", parent="root"),
            ev("e2", "create", "b", 1, "R2", name="b", parent="root"),
            ev("e3", "create", "c", 1, "R3", name="c", parent="root"),
            ev("e4", "move", "a", 2, "R1", deps=["e1", "e3"], parent="c"),
            ev("e5", "move", "b", 3, "R1", deps=["e2", "e1"], parent="a"),
            ev("e6", "move", "c", 4, "R1", deps=["e3", "e2"], parent="b"),
        ]
        result = check_convergence(events)
        self.assertEqual(result["status"], "converged")
        state = deliver_all(events).materialize()
        self.assertIsNone(sync._find_cycle(state.parents))
        # 环上最高戳移动 e6(clock 4) 被拒
        self.assertEqual([r["event"] for r in state.rejected], ["e6"])
        self.assertEqual(state.paths["c"], "/c")


class TombstoneTests(unittest.TestCase):
    def test_delete_tombstone_not_resurrected_by_old_events(self):
        """删除墓碑不被旧事件复活：先投删除，再投更旧的改名，对象保持已删。"""
        events = [
            ev("e1", "create", "a", 1, "R1", name="a.txt", parent="root"),
            ev("e2", "rename", "a", 2, "R2", name="ghost.txt"),   # 与删除并发但更旧
            ev("e3", "delete", "a", 3, "R1", deps=["e1"]),
        ]
        # 删除先投递，旧改名后投递
        state = deliver_all(events, ["e1", "e3", "e2"]).materialize()
        self.assertNotIn("a", state.paths)
        self.assertIn("a", state.tombstones)

    def test_newer_event_after_delete_still_applies(self):
        """比删除戳更新的事件（因果上在删除之后）仍然生效。"""
        events = [
            ev("e1", "create", "a", 1, "R1", name="a.txt", parent="root"),
            ev("e2", "delete", "a", 2, "R1", deps=["e1"]),
            ev("e3", "rename", "a", 3, "R1", deps=["e2"], name="back.txt"),
        ]
        state = deliver_all(events).materialize()
        self.assertEqual(state.paths["a"], "/back.txt")

    def test_delete_wins_over_concurrent_older_rename(self):
        events = [
            ev("e1", "create", "a", 1, "R1", name="a.txt", parent="root"),
            ev("e2", "rename", "a", 2, "R1", deps=["e1"], name="x.txt"),
            ev("e3", "delete", "a", 2, "R2", deps=["e1"]),
        ]
        result = check_convergence(events)
        self.assertEqual(result["status"], "converged")
        state = deliver_all(events).materialize()
        self.assertNotIn("a", state.paths)


class ConvergenceTests(unittest.TestCase):
    def _scenario(self):
        return [
            ev("e1", "create", "dir1", 1, "R1", name="docs", parent="root"),
            ev("e2", "create", "f1", 1, "R2", name="a.txt", parent="root"),
            ev("e3", "create", "f2", 2, "R2", deps=["e2"], name="b.txt",
               parent="root"),
            ev("e4", "move", "f1", 3, "R1", deps=["e1", "e2"], parent="dir1"),
            ev("e5", "rename", "f2", 3, "R2", deps=["e3"], name="a.txt"),
            ev("e6", "move", "f2", 4, "R1", deps=["e3", "e1"], parent="dir1"),
            ev("e7", "delete", "f1", 5, "R2", deps=["e4"]),
            ev("e8", "rename", "dir1", 6, "R1", deps=["e4"], name="papers"),
        ]

    def test_random_causal_orders_converge(self):
        """任意符合因果的投递顺序须收敛（200 个随机顺序）。"""
        events = self._scenario()
        reference = None
        for order in random_causal_orders(events, 200):
            canon = deliver_all(events, order).materialize().canonical()
            if reference is None:
                reference = canon
            self.assertEqual(canon, reference)

    def test_exhaustive_convergence(self):
        result = check_convergence(self._scenario())
        self.assertEqual(result["status"], "converged")
        self.assertGreater(result["orders_tested"], 100)

    def test_causal_violation_rejected(self):
        node = SyncNode()
        with self.assertRaises(ValueError):
            node.deliver(ev("e2", "rename", "a", 2, "R1", deps=["e1"],
                            name="x"))

    def test_duplicate_delivery_rejected(self):
        node = SyncNode()
        node.deliver(ev("e1", "create", "a", 1, "R1", name="a"))
        with self.assertRaises(ValueError):
            node.deliver(ev("e1", "create", "a", 1, "R1", name="a"))


class SemanticsTests(unittest.TestCase):
    def test_basic_create_move_rename_delete(self):
        events = [
            ev("e1", "create", "d", 1, "R1", name="dir", parent="root"),
            ev("e2", "create", "f", 2, "R1", deps=["e1"], name="f.txt",
               parent="d"),
            ev("e3", "rename", "f", 3, "R1", deps=["e2"], name="g.txt"),
            ev("e4", "move", "f", 4, "R1", deps=["e3"], parent="root"),
        ]
        state = deliver_all(events).materialize()
        self.assertEqual(state.paths["f"], "/g.txt")
        self.assertEqual(state.paths["d"], "/dir")

    def test_orphan_reattached_to_root(self):
        events = [
            ev("e1", "create", "d", 1, "R1", name="dir", parent="root"),
            ev("e2", "create", "f", 2, "R1", deps=["e1"], name="f.txt",
               parent="d"),
            ev("e3", "delete", "d", 3, "R1", deps=["e1"]),
        ]
        state = deliver_all(events).materialize()
        self.assertEqual(state.paths["f"], "/f.txt")
        self.assertEqual(state.orphans[0]["obj"], "f")

    def test_conflict_dir_disambiguation(self):
        """冲突目录内同名时按对象 ID 消歧。"""
        events = [
            ev("e1", "create", "x", 1, "R1", name="n", parent="root"),
            ev("e2", "create", "y", 1, "R2", name="n", parent="root"),
            ev("e3", "create", "z", 1, "R3", name="n", parent="root"),
        ]
        state = deliver_all(events).materialize()
        self.assertEqual(state.paths["z"], "/n")  # (1,"R3") 最高
        self.assertEqual(state.paths["x"], "/.conflicts/n#x")
        self.assertEqual(state.paths["y"], "/.conflicts/n#y")
        self.assertEqual(len(state.conflicts), 2)


class CliTests(unittest.TestCase):
    def _run_cli(self, events, *args):
        with tempfile.NamedTemporaryFile(
                "w", suffix=".json", delete=False) as fh:
            json.dump({"events": [
                {k: v for k, v in {
                    "id": e.id, "op": e.op, "obj": e.obj, "clock": e.clock,
                    "replica": e.replica, "deps": sorted(e.deps),
                    "name": e.name, "parent": e.parent,
                }.items() if v is not None and v != []}
                for e in events
            ]}, fh)
            path = fh.name
        try:
            return subprocess.run(
                [sys.executable, "sync.py", *args, path],
                capture_output=True, text=True,
                cwd=os.path.dirname(os.path.abspath(__file__)))
        finally:
            os.unlink(path)

    def test_cli_apply(self):
        events = [
            ev("e1", "create", "a", 1, "R1", name="a.txt", parent="root"),
            ev("e2", "create", "b", 1, "R2", name="b.txt", parent="root"),
            ev("e3", "rename", "a", 2, "R1", deps=["e1"], name="same.txt"),
            ev("e4", "rename", "b", 2, "R2", deps=["e2"], name="same.txt"),
        ]
        proc = self._run_cli(events, "apply")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn("/same.txt", proc.stdout)
        self.assertIn("name-conflict", proc.stdout)

    def test_cli_converge(self):
        events = [
            ev("e1", "create", "a", 1, "R1", name="a", parent="root"),
            ev("e2", "create", "b", 2, "R1", deps=["e1"], name="b", parent="a"),
            ev("e3", "move", "a", 3, "R1", deps=["e2"], parent="b"),
        ]
        proc = self._run_cli(events, "converge")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn("converged", proc.stdout)
        self.assertIn("cycle", proc.stdout)

    def test_cli_rejects_causal_violation(self):
        events = [ev("e1", "rename", "a", 1, "R1", deps=["nope"], name="x")]
        with tempfile.NamedTemporaryFile(
                "w", suffix=".json", delete=False) as fh:
            json.dump({"events": [
                {"id": "e1", "op": "rename", "obj": "a", "clock": 1,
                 "replica": "R1", "deps": ["e0"], "name": "x"}]}, fh)
            path = fh.name
        try:
            proc = subprocess.run(
                [sys.executable, "sync.py", "apply", path],
                capture_output=True, text=True,
                cwd=os.path.dirname(os.path.abspath(__file__)))
        finally:
            os.unlink(path)
        self.assertEqual(proc.returncode, 2)
        self.assertIn("unknown events", proc.stderr)


if __name__ == "__main__":
    unittest.main()
