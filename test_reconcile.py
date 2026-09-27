"""reconcile 的单元测试。所有数据均为合成数据。"""

import unittest

from reconcile import Store, sync, merged_store, live_entry, tombstone_entry


def make_store(entries=(), tombstones=()):
    s = Store()
    for key, version, value in entries:
        s.put(key, version, value)
    for key, version in tombstones:
        s.delete(key, version)
    return s


class TestProbabilisticMode(unittest.TestCase):
    def test_identical_stores_no_diffs_low_traffic(self):
        a = make_store([("k1", 1, "x"), ("k2", 2, "y")])
        b = make_store([("k1", 1, "x"), ("k2", 2, "y")])
        r = sync(a, b, mode="probabilistic")
        self.assertEqual(r["differences"], [])
        self.assertEqual(r["stats"]["entries_transmitted"], 0)

    def test_constant_hash_collision_misses_difference(self):
        # 概率模式 + 恒定哈希：同键不同值被碰撞掩盖，明确展示概率模式的局限
        a = make_store([("k1", 1, "alpha")])
        b = make_store([("k1", 1, "beta")])
        r = sync(a, b, mode="probabilistic", hash_name="constant")
        self.assertEqual(r["differences"], [])
        self.assertEqual(r["stats"]["entries_transmitted"], 0)

    def test_real_hash_detects_difference_incrementally(self):
        a = make_store([("k1", 1, "alpha"), ("k2", 1, "same")])
        b = make_store([("k1", 1, "beta"), ("k2", 1, "same")])
        r = sync(a, b, mode="probabilistic")
        self.assertEqual([d["key"] for d in r["differences"]], ["k1"])
        # 增量性：只传输差异子树中的条目，而非全量
        self.assertLess(r["stats"]["entries_transmitted"], 4)


class TestExactMode(unittest.TestCase):
    def test_acceptance_same_key_different_value_constant_hash(self):
        # 验收边界 1：同键不同值 + 恒定哈希，精确模式必须报告差异
        a = make_store([("k1", 1, "alpha")])
        b = make_store([("k1", 1, "beta")])
        r = sync(a, b, mode="exact", hash_name="constant")
        self.assertEqual(len(r["differences"]), 1)
        d = r["differences"][0]
        self.assertEqual(d["key"], "k1")
        self.assertEqual(d["local"]["value"], "alpha")
        self.assertEqual(d["remote"]["value"], "beta")
        self.assertIn(d["resolution"]["value"], ("alpha", "beta"))

    def test_exact_mode_reports_all_diffs_with_constant_hash(self):
        a = make_store([("a", 1, "1"), ("b", 2, "2"), ("c", 1, "x")])
        b = make_store([("b", 2, "2"), ("c", 1, "y"), ("d", 5, "4")])
        r = sync(a, b, mode="exact", hash_name="constant")
        self.assertEqual([d["key"] for d in r["differences"]], ["a", "c", "d"])

    def test_exact_mode_identical_stores(self):
        a = make_store([("k1", 1, "x")], [("gone", 3)])
        b = make_store([("k1", 1, "x")], [("gone", 3)])
        r = sync(a, b, mode="exact", hash_name="constant")
        self.assertEqual(r["differences"], [])
        # 精确模式必须传输条目以核验（恒定哈希下无法信任摘要）
        self.assertGreater(r["stats"]["entries_transmitted"], 0)


class TestMergeRules(unittest.TestCase):
    def test_acceptance_tombstone_beats_lower_version_live(self):
        # 验收边界 2：墓碑版本高于存活值，合并应删除
        a = make_store([("k1", 2, "alive")])
        b = make_store(tombstones=[("k1", 5)])
        m = merged_store(a, b)
        self.assertNotIn("k1", m.entries)
        self.assertIn("k1", m.tombstones)
        self.assertEqual(m.tombstones["k1"]["version"], 5)

    def test_live_beats_lower_version_tombstone(self):
        a = make_store([("k1", 7, "alive")])
        b = make_store(tombstones=[("k1", 5)])
        m = merged_store(a, b)
        self.assertEqual(m.entries["k1"]["value"], "alive")
        self.assertNotIn("k1", m.tombstones)

    def test_same_version_tombstone_wins(self):
        a = make_store([("k1", 3, "alive")])
        b = make_store(tombstones=[("k1", 3)])
        m = merged_store(a, b)
        self.assertIn("k1", m.tombstones)
        self.assertNotIn("k1", m.entries)

    def test_store_delete_respects_versions(self):
        s = make_store([("k1", 5, "v")])
        s.delete("k1", 3)  # 低版本墓碑不生效
        self.assertIn("k1", s.entries)
        s.delete("k1", 9)
        self.assertNotIn("k1", s.entries)
        self.assertEqual(s.tombstones["k1"]["version"], 9)
        s.put("k1", 6, "old")  # 低于墓碑版本，不复活
        self.assertNotIn("k1", s.entries)
        s.put("k1", 10, "new")  # 高于墓碑版本，复活
        self.assertEqual(s.entries["k1"]["value"], "new")

    def test_sync_merged_converges(self):
        a = make_store([("k1", 1, "x"), ("k2", 4, "keep")], [("k3", 6)])
        b = make_store([("k1", 2, "y"), ("k3", 3, "z")])
        r = sync(a, b, mode="exact")
        m = r["merged"]
        vals = {e["key"]: e["value"] for e in m["entries"]}
        self.assertEqual(vals, {"k1": "y", "k2": "keep"})
        self.assertEqual([t["key"] for t in m["tombstones"]], ["k3"])


class TestStatsAndFormat(unittest.TestCase):
    def test_stats_fields_and_mode_recorded(self):
        a = make_store([("k1", 1, "x")])
        b = make_store([("k2", 1, "y")])
        r = sync(a, b, mode="exact")
        st = r["stats"]
        self.assertEqual(st["mode"], "exact")
        self.assertEqual(st["entries_transmitted"], 2)
        self.assertGreater(st["digest_comparisons"], 0)
        self.assertEqual(r["mode"], "exact")
        self.assertIn("mode_semantics", r)

    def test_diff_list_is_reviewable(self):
        a = make_store([("k1", 1, "x")])
        b = make_store(tombstones=[("k1", 2)])
        r = sync(a, b, mode="exact")
        d = r["differences"][0]
        self.assertEqual(set(d), {"key", "local", "remote", "resolution"})
        self.assertEqual(d["resolution"], tombstone_entry("k1", 2))

    def test_empty_stores(self):
        r = sync(Store(), Store(), mode="exact")
        self.assertEqual(r["differences"], [])
        self.assertEqual(r["merged"], {"entries": [], "tombstones": []})


if __name__ == "__main__":
    unittest.main()
