"""unittest 测试套件：不可变段 + 删除位图索引。

运行：python3.11 -m unittest -v
"""

import threading
import time
import unittest

from index import Index
from tokenizer import tokenize


class TestTokenize(unittest.TestCase):
    def test_ascii_alnum_sequences_lowercased(self):
        self.assertEqual(tokenize("Hello World"), ["hello", "world"])
        self.assertEqual(tokenize("Python3.11"), ["python3", "11"])

    def test_cjk_single_char_tokens(self):
        self.assertEqual(tokenize("水果"), ["水", "果"])
        self.assertEqual(tokenize("苹果和香蕉"), ["苹", "果", "和", "香", "蕉"])

    def test_mixed_and_separators(self):
        self.assertEqual(
            tokenize("Hello, 世界 123!"),
            ["hello", "世", "界", "123"],
        )
        self.assertEqual(tokenize("，。！ \t\n"), [])


class TestBasicIndex(unittest.TestCase):
    def test_put_and_search(self):
        idx = Index()
        idx.put("d1", "苹果和香蕉")
        idx.put("d2", "apple and banana")
        self.assertEqual(idx.search("苹"), ["d1"])
        self.assertEqual(idx.search("apple"), ["d2"])
        self.assertEqual(idx.search("不存在的词"), [])

    def test_search_and_semantics(self):
        idx = Index()
        idx.put("d1", "苹果 香蕉")
        idx.put("d2", "苹果 橙子")
        self.assertEqual(idx.search("苹 蕉"), ["d1"])
        self.assertEqual(idx.search("苹"), ["d1", "d2"])

    def test_get_missing_returns_none(self):
        idx = Index()
        self.assertIsNone(idx.get("nope"))

    def test_delete_unknown_returns_false(self):
        idx = Index()
        self.assertFalse(idx.delete("nope"))


class TestVersionShadowing(unittest.TestCase):
    def test_new_version_shadows_old(self):
        idx = Index()
        idx.put("d1", "旧版本 香蕉")
        idx.put("d1", "新版本 葡萄")
        self.assertEqual(idx.get("d1"), "新版本 葡萄")
        self.assertEqual(idx.search("葡萄"), ["d1"])
        self.assertEqual(idx.search("蕉"), [])  # 旧版本词元不再命中

    def test_stats_count_only_visible(self):
        idx = Index()
        idx.put("d1", "第一版")
        idx.put("d1", "第二版")
        idx.put("d2", "另一篇")
        idx.delete("d2")
        stats = idx.stats()
        self.assertEqual(stats["visible_docs"], 1)  # d1 只算一次，d2 被删除
        self.assertEqual(stats["segments"], 4)

    def test_delete_then_readd(self):
        idx = Index()
        idx.put("d1", "原始内容")
        idx.delete("d1")
        self.assertIsNone(idx.get("d1"))
        idx.put("d1", "重新上架")
        self.assertEqual(idx.get("d1"), "重新上架")
        self.assertEqual(idx.stats()["visible_docs"], 1)


class TestSnapshotIsolation(unittest.TestCase):
    def test_old_snapshot_sees_old_data(self):
        idx = Index()
        idx.put("d1", "旧内容 苹果")
        snap = idx.snapshot()
        idx.put("d1", "新内容 葡萄")
        idx.delete("d1")
        self.assertEqual(snap.get("d1"), "旧内容 苹果")
        self.assertEqual(snap.search("苹"), ["d1"])
        self.assertEqual(snap.stats()["visible_docs"], 1)
        self.assertIsNone(idx.get("d1"))

    def test_snapshot_survives_merge(self):
        idx = Index()
        idx.put("d1", "甲")
        idx.put("d2", "乙")
        snap = idx.snapshot()
        idx.merge_all()
        self.assertEqual(idx.segment_count, 1)
        self.assertEqual(snap.get("d1"), "甲")  # 旧读者仍持有旧段
        self.assertEqual(len(snap.segments), 2)


class TestMerge(unittest.TestCase):
    def test_merge_all_preserves_visible_data(self):
        idx = Index()
        idx.put("d1", "苹果")
        idx.put("d2", "香蕉")
        idx.put("d1", "苹果第二版")
        idx.delete("d2")
        idx.put("d3", "橙子")
        before = (idx.get("d1"), idx.search("苹"), idx.stats()["visible_docs"])
        merged = idx.merge_all()
        self.assertIsNotNone(merged)
        self.assertEqual(idx.segment_count, 1)
        after = (idx.get("d1"), idx.search("苹"), idx.stats()["visible_docs"])
        self.assertEqual(before, after)
        # 合并后段内只剩可见数据：d1 最高版本 + d3
        self.assertEqual(set(merged.docs), {"d1", "d3"})
        self.assertEqual(merged.docs["d1"][0], 2)
        self.assertEqual(merged.deleted, {})  # 墓碑已覆盖所有旧段，可丢弃

    def test_merge_oldest_keeps_newest_segment(self):
        idx = Index()
        idx.put("d1", "一")
        idx.put("d2", "二")
        idx.put("d3", "三")
        idx.merge_oldest(k=2)
        self.assertEqual(idx.segment_count, 2)
        self.assertEqual(idx.search("三"), ["d3"])
        self.assertEqual(idx.search("一"), ["d1"])

    def test_tombstone_kept_until_all_old_segments_covered(self):
        idx = Index()
        idx.put("a", "旧甲")   # 段0: a@1
        idx.put("b", "乙")     # 段1
        idx.delete("a")        # 段2: 墓碑 a@1
        # 合并最新两段（段2+段1），段0 未参与，仍含 a@1 -> 墓碑必须保留
        merged = idx.merge_newest(k=2)
        self.assertEqual(merged.deleted, {"a": 1})
        self.assertIsNone(idx.get("a"))  # 墓碑仍然生效
        # 再合并全部段，墓碑覆盖所有旧段 -> 安全丢弃
        final = idx.merge_all()
        self.assertEqual(final.deleted, {})
        self.assertEqual(final.docs, {"b": (1, "乙")})
        self.assertIsNone(idx.get("a"))

    def test_concurrent_update_during_merge_not_lost(self):
        idx = Index()
        idx.put("x", "初版")
        idx.put("y", "另一篇")
        old_snap = idx.snapshot()

        def write_during_merge():
            idx.put("x", "合并期间的新值")

        idx._merge_hook = write_during_merge
        idx.merge_all()
        idx._merge_hook = None
        # 合并期间写入的新值落在更新的段里，未被合并丢弃
        self.assertEqual(idx.get("x"), "合并期间的新值")
        self.assertEqual(idx.segment_count, 2)  # 合并段 + 合并期间的新段
        # 旧读者仍可查询旧段
        self.assertEqual(old_snap.get("x"), "初版")

    def test_concurrent_delete_during_merge_not_lost(self):
        idx = Index()
        idx.put("x", "将被删除")
        idx.put("y", "保留")

        def delete_during_merge():
            idx.delete("x")

        idx._merge_hook = delete_during_merge
        idx.merge_all()
        idx._merge_hook = None
        self.assertIsNone(idx.get("x"))
        self.assertEqual(idx.get("y"), "保留")
        self.assertEqual(idx.stats()["visible_docs"], 1)


class TestBackgroundMerge(unittest.TestCase):
    def test_background_merge_bounds_segment_count(self):
        idx = Index(merge_threshold=4)
        idx.start_background_merge(interval=0.005)
        try:
            for i in range(50):
                idx.put(f"doc{i}", f"第{i}号文档 水果")
            deadline = time.time() + 5
            while idx.segment_count > 5 and time.time() < deadline:
                time.sleep(0.01)
            self.assertLessEqual(idx.segment_count, 5)
            self.assertEqual(len(idx.search("水果")), 50)
            self.assertEqual(idx.stats()["visible_docs"], 50)
        finally:
            idx.stop_background_merge()

    def test_writes_during_background_merge_are_visible(self):
        idx = Index(merge_threshold=2)
        idx.start_background_merge(interval=0.001)
        try:
            for i in range(100):
                idx.put("hot", f"版本{i}")
                self.assertEqual(idx.get("hot"), f"版本{i}")
            self.assertEqual(idx.stats()["visible_docs"], 1)
        finally:
            idx.stop_background_merge()

    def test_multithreaded_readers_consistent(self):
        idx = Index(merge_threshold=3)
        idx.start_background_merge(interval=0.001)
        errors = []
        try:
            for i in range(20):
                idx.put(f"doc{i}", f"文档{i}号 水果")

            def reader():
                try:
                    for _ in range(50):
                        hits = idx.search("水果")
                        if hits != sorted(hits) or len(hits) > 20:
                            errors.append(f"bad hits: {hits}")
                        stats = idx.stats()
                        if not 0 <= stats["visible_docs"] <= 20:
                            errors.append(f"bad stats: {stats}")
                except Exception as exc:  # pragma: no cover
                    errors.append(repr(exc))

            threads = [threading.Thread(target=reader) for _ in range(4)]
            for t in threads:
                t.start()
            for t in threads:
                t.join()
            self.assertEqual(errors, [])
        finally:
            idx.stop_background_merge()


if __name__ == "__main__":
    unittest.main()
