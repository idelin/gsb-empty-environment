import os
import random
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from attribution.engine import Attribution, Engine, full_recompute
from attribution.journal import PersistentEngine


def tp(eid, subj, t, ch, v=1):
    return {"op": "event", "event_id": eid, "version": v, "subject": subj,
            "time": t, "kind": "touchpoint", "channel": ch}


def cv(eid, subj, t, amt, v=1):
    return {"op": "event", "event_id": eid, "version": v, "subject": subj,
            "time": t, "kind": "conversion", "amount": amt}


def edge_add(a, b, v=1):
    return {"op": "add_edge", "a": a, "b": b, "version": v}


def edge_del(a, b):
    return {"op": "remove_edge", "a": a, "b": b}


class TestWindowAttribution(unittest.TestCase):
    def test_left_boundary_closed(self):
        # 验收边界：触点a在2、成交在5、窗口3，左边界闭 -> 包含a
        e = Engine(3)
        e.apply(tp("t1", "u1", 2, "a"))
        changes = e.apply(cv("c1", "u1", 5, 100))
        self.assertEqual(e.attributions["c1"], Attribution("t1", "a", 100))
        self.assertEqual(changes[0]["new_channel"], "a")

    def test_outside_left_boundary_excluded(self):
        e = Engine(3)
        e.apply(tp("t1", "u1", 1, "a"))  # 5-3=2，1 在窗口外
        e.apply(cv("c1", "u1", 5, 100))
        self.assertIsNone(e.attributions["c1"].channel)

    def test_late_touchpoint_reattributes(self):
        # 验收边界：新增触点b在4，成交从a改归b
        e = Engine(3)
        e.apply(tp("t1", "u1", 2, "a"))
        e.apply(cv("c1", "u1", 5, 100))
        changes = e.apply(tp("t2", "u1", 4, "b"))
        self.assertEqual(len(changes), 1)
        ch = changes[0]
        self.assertEqual((ch["conversion"], ch["old_channel"], ch["new_channel"]),
                         ("c1", "a", "b"))
        self.assertEqual(e.attributions["c1"].touchpoint_id, "t2")

    def test_late_touchpoint_outside_any_window_changes_nothing(self):
        e = Engine(3)
        e.apply(tp("t1", "u1", 2, "a"))
        e.apply(cv("c1", "u1", 5, 100))
        changes = e.apply(tp("t2", "u1", 9, "b"))  # 不在 [2,5] 内
        self.assertEqual(changes, [])
        self.assertEqual(e.attributions["c1"].channel, "a")

    def test_tie_breaks_by_min_touchpoint_id(self):
        e = Engine(3)
        e.apply(tp("t9", "u1", 4, "x"))
        e.apply(tp("t3", "u1", 4, "y"))
        e.apply(cv("c1", "u1", 5, 100))
        self.assertEqual(e.attributions["c1"].touchpoint_id, "t3")

    def test_multiple_conversions_independent(self):
        e = Engine(3)
        e.apply(tp("t1", "u1", 1, "a"))
        e.apply(tp("t2", "u1", 4, "b"))
        e.apply(cv("c1", "u1", 5, 100))   # 窗口[2,5] -> b
        e.apply(cv("c2", "u1", 3, 50))    # 窗口[0,3] -> a
        e.apply(cv("c3", "u1", 20, 70))   # 窗口[17,20] -> 无
        self.assertEqual(e.attributions["c1"].channel, "b")
        self.assertEqual(e.attributions["c2"].channel, "a")
        self.assertIsNone(e.attributions["c3"].channel)


class TestRevisions(unittest.TestCase):
    def test_touchpoint_revision_overwrites_channel(self):
        e = Engine(3)
        e.apply(tp("t1", "u1", 2, "a", v=1))
        e.apply(cv("c1", "u1", 5, 100))
        changes = e.apply(tp("t1", "u1", 2, "z", v=2))
        self.assertEqual(changes[0]["new_channel"], "z")
        self.assertEqual(e.attributions["c1"].channel, "z")

    def test_touchpoint_revision_moves_out_of_window(self):
        e = Engine(3)
        e.apply(tp("t1", "u1", 2, "a", v=1))
        e.apply(cv("c1", "u1", 5, 100))
        changes = e.apply(tp("t1", "u1", 99, "a", v=2))
        self.assertEqual(changes[0]["new_channel"], None)
        self.assertIsNone(e.attributions["c1"].channel)

    def test_stale_version_ignored(self):
        e = Engine(3)
        e.apply(tp("t1", "u1", 2, "a", v=2))
        changes = e.apply(tp("t1", "u1", 2, "z", v=1))
        self.assertEqual(changes, [])
        e.apply(cv("c1", "u1", 5, 100))
        self.assertEqual(e.attributions["c1"].channel, "a")

    def test_conversion_revision_updates_amount(self):
        e = Engine(3)
        e.apply(tp("t1", "u1", 2, "a"))
        e.apply(cv("c1", "u1", 5, 100, v=1))
        changes = e.apply(cv("c1", "u1", 5, 250, v=2))
        self.assertEqual(changes[0]["amount"], 250)
        self.assertEqual(e.attributions["c1"].amount, 250)
        self.assertEqual(e.attributions["c1"].channel, "a")

    def test_amount_must_be_int(self):
        e = Engine(3)
        with self.assertRaises(ValueError):
            e.apply(cv("c1", "u1", 5, 10.5))


class TestIdentityGraph(unittest.TestCase):
    def test_merge_attributes_across_devices(self):
        e = Engine(3)
        e.apply(cv("c1", "u1", 5, 100))
        e.apply(tp("t1", "u2", 4, "b"))
        self.assertIsNone(e.attributions["c1"].channel)
        changes = e.apply(edge_add("u1", "u2"))
        self.assertEqual(e.attributions["c1"].channel, "b")
        identity = [c for c in changes if c["type"] == "identity_change"][0]
        self.assertEqual(identity["added"], {"b": 100})
        self.assertTrue(identity["conserved"])

    def test_split_revokes_cross_device_attribution(self):
        e = Engine(3)
        e.apply(cv("c1", "u1", 5, 100))
        e.apply(tp("t1", "u2", 4, "b"))
        e.apply(edge_add("u1", "u2"))
        self.assertEqual(e.attributions["c1"].channel, "b")
        changes = e.apply(edge_del("u1", "u2"))
        self.assertIsNone(e.attributions["c1"].channel)
        identity = [c for c in changes if c["type"] == "identity_change"][0]
        self.assertEqual(identity["revoked"], {"b": 100})
        self.assertTrue(identity["conserved"])

    def test_merge_conservation_with_reattribution(self):
        e = Engine(3)
        e.apply(tp("t1", "u1", 2, "a"))
        e.apply(cv("c1", "u1", 5, 100))   # -> a
        e.apply(tp("t2", "u2", 4, "b"))
        e.apply(cv("c2", "u2", 6, 50))    # -> b
        changes = e.apply(edge_add("u1", "u2"))
        # c1 窗口[2,5] 现在含 t2@4 -> 改归 b；c2 窗口[3,6] 仍为 b
        self.assertEqual(e.attributions["c1"].channel, "b")
        self.assertEqual(e.attributions["c2"].channel, "b")
        identity = [c for c in changes if c["type"] == "identity_change"][0]
        self.assertEqual(identity["before"], {"a": 100, "b": 50})
        self.assertEqual(identity["after"], {"b": 150})
        self.assertEqual(identity["added"], {"b": 100})
        self.assertEqual(identity["revoked"], {"a": 100})
        self.assertTrue(identity["conserved"])

    def test_transitive_component(self):
        e = Engine(10)
        e.apply(tp("t1", "u3", 1, "z"))
        e.apply(cv("c1", "u1", 5, 10))
        e.apply(edge_add("u1", "u2"))
        self.assertIsNone(e.attributions["c1"].channel)
        e.apply(edge_add("u2", "u3"))
        self.assertEqual(e.attributions["c1"].channel, "z")

    def test_query_at_preserves_old_identity(self):
        e = Engine(3)
        e.apply(cv("c1", "u1", 5, 100))          # seq 1
        e.apply(tp("t1", "u2", 4, "b"))          # seq 2
        e.apply(edge_add("u1", "u2"))            # seq 3
        self.assertIsNone(e.query_at(2)["c1"].channel)   # 当时未合并
        self.assertEqual(e.query_at(3)["c1"].channel, "b")
        self.assertEqual(e.attributions["c1"].channel, "b")
        e.apply(edge_del("u1", "u2"))            # seq 4
        self.assertEqual(e.query_at(3)["c1"].channel, "b")  # 旧版本不受拆分影响
        self.assertIsNone(e.attributions["c1"].channel)


class TestJournal(unittest.TestCase):
    def test_recovery_and_idempotency(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "journal.log")
            pe = PersistentEngine(3, path)
            pe.apply("k1", tp("t1", "u1", 2, "a"))
            pe.apply("k2", cv("c1", "u1", 5, 100))
            self.assertEqual(pe.apply("k2", cv("c1", "u1", 5, 100)), [])  # 重复键不生效
            c3 = pe.apply("k3", tp("t2", "u1", 4, "b"))
            self.assertEqual(c3[0]["new_channel"], "b")

            pe2 = PersistentEngine(3, path)  # 崩溃后恢复
            self.assertEqual(pe2.attributions, pe.attributions)
            self.assertEqual(pe2.apply("k3", tp("t2", "u1", 4, "b")), [])  # 恢复后仍幂等

            records = pe2.journal.replay()
            self.assertEqual([r["key"] for r in records], ["k1", "k2", "k3"])
            # 幂等键与归因变更在同一记录中
            self.assertEqual(records[2]["changes"], c3)

    def test_torn_tail_discarded(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "journal.log")
            pe = PersistentEngine(3, path)
            pe.apply("k1", tp("t1", "u1", 2, "a"))
            pe.apply("k2", cv("c1", "u1", 5, 100))
            with open(path, "a", encoding="utf-8") as fh:  # 模拟崩溃写坏尾部
                fh.write('{"crc": 123, "payl')
            pe2 = PersistentEngine(3, path)
            self.assertEqual(len(pe2.journal.replay()), 2)
            self.assertEqual(pe2.attributions["c1"].channel, "a")
            pe2.apply("k3", tp("t2", "u1", 4, "b"))  # 恢复后可继续写入
            pe3 = PersistentEngine(3, path)
            self.assertEqual(pe3.attributions["c1"].channel, "b")


class TestRandomizedConsistency(unittest.TestCase):
    def test_incremental_matches_full_recompute(self):
        for seed in range(30):
            rng = random.Random(seed)
            window = 5
            engine = Engine(window)
            versions = {}
            active_edges = set()
            subjects = [f"s{i}" for i in range(6)]
            channels = ["a", "b", "c", "d"]
            tp_ids, conv_ids, ops = [], [], []
            for _ in range(120):
                r = rng.random()
                if r < 0.40:  # 触点（含同业务时间）
                    eid = f"t{len(tp_ids)}"
                    tp_ids.append(eid)
                    versions[eid] = 1
                    op = tp(eid, rng.choice(subjects), rng.randint(0, 30), rng.choice(channels))
                elif r < 0.60:  # 成交
                    eid = f"c{len(conv_ids)}"
                    conv_ids.append(eid)
                    versions[eid] = 1
                    op = cv(eid, rng.choice(subjects), rng.randint(0, 30), rng.randint(1, 500))
                elif r < 0.75 and (tp_ids or conv_ids):  # 事件修订
                    eid = rng.choice(tp_ids + conv_ids)
                    versions[eid] += 1
                    if eid.startswith("t"):
                        op = tp(eid, rng.choice(subjects), rng.randint(0, 30),
                                rng.choice(channels), v=versions[eid])
                    else:
                        op = cv(eid, rng.choice(subjects), rng.randint(0, 30),
                                rng.randint(1, 500), v=versions[eid])
                elif r < 0.90:  # 边新增
                    a, b = rng.sample(subjects, 2)
                    key = tuple(sorted((a, b)))
                    if key in active_edges:
                        continue
                    active_edges.add(key)
                    op = edge_add(a, b)
                else:  # 边删除
                    if not active_edges:
                        continue
                    a, b = rng.choice(sorted(active_edges))
                    active_edges.discard((a, b))
                    op = edge_del(a, b)
                engine.apply(op)
                ops.append(op)
            expected = full_recompute(ops, window)
            self.assertEqual(engine.attributions, expected, f"seed={seed} 终态不一致")
            for _ in range(5):  # 旧版本查询与截断全量一致
                s = rng.randint(1, len(ops))
                self.assertEqual(engine.query_at(s), full_recompute(ops[:s], window),
                                 f"seed={seed} seq={s} 历史版本不一致")


if __name__ == "__main__":
    unittest.main()
