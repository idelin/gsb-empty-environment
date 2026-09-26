import threading
import unittest

from lsm_index import DocRecord, LsmTextIndex, tokenize


class TestTokenizer(unittest.TestCase):
    def test_utf8_bytes_and_casefold(self):
        self.assertEqual(tokenize("Hello HELLO"), ["hello", "hello"])
        self.assertEqual(tokenize("Straße".encode("utf-8")), ["strasse"])

    def test_invalid_utf8_raises(self):
        with self.assertRaises(UnicodeDecodeError):
            tokenize(b"\xff\xfe invalid")

    def test_cjk_per_character(self):
        self.assertEqual(tokenize("后台合并"), ["后", "台", "合", "并"])

    def test_alnum_runs_and_punctuation(self):
        self.assertEqual(tokenize("seg-12, v2!"), ["seg", "12", "v2"])

    def test_mixed(self):
        self.assertEqual(
            tokenize("合并 segments，go"), ["合", "并", "segments", "go"]
        )

    def test_deterministic(self):
        t = "Background Merge, 后台合并!"
        self.assertEqual(tokenize(t), tokenize(t))


class TestReadWrite(unittest.TestCase):
    def _populate_three_segments(self):
        idx = LsmTextIndex()
        idx.upsert("a", "apple banana")
        idx.upsert("b", "banana cherry")
        idx.flush()
        idx.upsert("c", "cherry apple date")
        idx.flush()
        idx.upsert("a", "apple banana updated")  # 更新遮蔽旧版本
        idx.delete("b")                            # 墓碑遮蔽
        idx.flush()
        return idx

    def test_shadowing_and_visible_count(self):
        idx = self._populate_three_segments()
        self.assertEqual(len(idx.active_segments()), 3)
        snap = idx.open_snapshot()
        self.assertEqual(snap.count_visible(), 2)  # a(新), c；b 已删
        self.assertIsNone(snap.get_doc("b"))
        self.assertEqual(snap.get_doc("a"), "apple banana updated")
        self.assertIn("a", snap.search("apple updated"))
        self.assertIn("c", snap.search("cherry"))
        self.assertNotIn("b", snap.search("banana"))
        snap.close()

    def test_stats_count_only_visible(self):
        idx = self._populate_three_segments()
        snap = idx.open_snapshot()
        st = snap.stats("apple banana")
        self.assertEqual(st["visible_docs"], 2)
        self.assertEqual(st["terms"]["apple"]["doc_freq"], 2)  # a 与 c，各只算一次
        self.assertEqual(st["terms"]["banana"]["doc_freq"], 1)  # 旧 a 被遮蔽、b 被删
        snap.close()

    def test_readd_after_delete_unhides(self):
        idx = LsmTextIndex()
        idx.upsert("a", "v1 text")
        idx.flush()
        idx.delete("a")
        idx.flush()
        self.assertIsNone(idx.open_snapshot().get_doc("a"))
        idx.upsert("a", "v2 resurrected")
        idx.flush()
        snap = idx.open_snapshot()
        self.assertEqual(snap.get_doc("a"), "v2 resurrected")
        self.assertEqual(snap.count_visible(), 1)
        snap.close()

    def test_and_search_across_segments(self):
        idx = LsmTextIndex()
        idx.upsert("a", "red blue")
        idx.flush()
        idx.upsert("b", "red green blue shade")
        idx.upsert("c", "red")
        idx.flush()
        snap = idx.open_snapshot()
        self.assertEqual(snap.search("red blue"), ["a", "b"])
        self.assertEqual(snap.search("red green"), ["b"])
        self.assertEqual(snap.search("red xyz"), [])
        snap.close()


class TestSnapshotIsolation(unittest.TestCase):
    def test_old_reader_sees_old_segments(self):
        idx = LsmTextIndex()
        idx.upsert("a", "old value")
        idx.flush()
        old = idx.open_snapshot()

        idx.upsert("a", "new value")
        idx.flush()
        idx.delete("a")
        idx.flush()

        fresh = idx.open_snapshot()
        self.assertEqual(old.get_doc("a"), "old value")
        self.assertEqual(old.count_visible(), 1)
        self.assertIsNone(fresh.get_doc("a"))
        self.assertEqual(fresh.count_visible(), 0)
        fresh.close()
        old.close()

    def test_retired_segments_kept_until_reader_gone(self):
        idx = LsmTextIndex()
        idx.upsert("a", "one")
        idx.flush()
        idx.upsert("b", "two")
        idx.flush()
        old = idx.open_snapshot()
        idx.merge()
        self.assertEqual(idx.retired_count(), 2)  # 旧段为旧读者保留
        self.assertEqual(old.get_doc("a"), "one")
        old.close()
        self.assertEqual(idx.retired_count(), 0)  # 读者释放后才可丢


class TestMerge(unittest.TestCase):
    def test_full_merge_preserves_view(self):
        idx = LsmTextIndex()
        idx.upsert("a", "alpha beta")
        idx.flush()
        idx.upsert("b", "beta gamma")
        idx.flush()
        idx.delete("a")
        idx.upsert("c", "gamma alpha")
        idx.flush()
        before = idx.open_snapshot()
        before_ids = before.visible_doc_ids()
        before_search = before.search("gamma")
        seg_before = before.segments
        before.close()

        idx.merge()
        self.assertEqual(len(idx.active_segments()), 1)
        after = idx.open_snapshot()
        self.assertEqual(after.visible_doc_ids(), before_ids)
        self.assertEqual(after.search("gamma"), before_search)
        self.assertEqual(after.count_visible(), 2)
        self.assertIsNone(after.get_doc("a"))
        after.close()
        del seg_before

    def test_tombstone_carried_when_older_segment_remains(self):
        idx = LsmTextIndex()
        idx.upsert("a", "alpha")
        idx.upsert("b", "beta")
        idx.flush()  # s1 (older)
        idx.delete("a")
        idx.flush()  # s2
        idx.upsert("c", "gamma")
        idx.flush()  # s3
        ids = [s[0] for s in idx.active_segments()]

        # 只合并最新的两段 s2+s3；s1 中仍有 a 的旧活值 -> 墓碑必须保留
        idx.merge([ids[1], ids[2]])
        snap = idx.open_snapshot()
        self.assertIsNone(snap.get_doc("a"))
        self.assertEqual(snap.count_visible(), 2)  # b, c
        # 合并段含墓碑 a 与 c 共 2 条记录
        newest = max(snap.segments, key=lambda s: s[0])
        self.assertEqual(newest[1], 2)
        self.assertEqual(newest[2], 1)  # 仅 c 可见，a 是墓碑
        snap.close()

        # 全部合并后墓碑可丢，但 a 依然查不到
        idx.merge()
        snap = idx.open_snapshot()
        self.assertIsNone(snap.get_doc("a"))
        self.assertEqual(snap.count_visible(), 2)
        only = snap.segments[0]
        self.assertEqual({doc for doc in ("b", "c")}, set(snap.visible_doc_ids()))
        snap.close()

    def test_merge_all_drops_tombstone_when_nothing_to_shadow(self):
        idx = LsmTextIndex()
        idx.upsert("a", "alpha")
        idx.flush()
        idx.delete("a")
        idx.flush()
        merged = idx.merge()  # 没有更旧段需要遮蔽 -> 结果为空
        self.assertIsNone(merged)
        self.assertEqual(idx.active_segments(), [])
        snap = idx.open_snapshot()
        self.assertEqual(snap.count_visible(), 0)
        self.assertIsNone(snap.get_doc("a"))
        snap.close()

    def test_merge_rejects_noncontiguous_or_missing(self):
        idx = LsmTextIndex()
        for i in range(3):
            idx.upsert(f"d{i}", f"text {i}")
            idx.flush()
        ids = [s[0] for s in idx.active_segments()]
        with self.assertRaises(ValueError):
            idx.merge([ids[0], ids[2]])  # 不连续
        with self.assertRaises(ValueError):
            idx.merge([999])
        self.assertEqual(idx.merge([ids[0]]).seg_id, ids[0])  # 单段直接返回

    def test_concurrent_update_during_merge_keeps_new_value(self):
        idx = LsmTextIndex()
        for i in range(4):
            idx.upsert(f"d{i}", f"value {i} marker")
            idx.flush()

        def during_build(index: LsmTextIndex) -> None:
            # 构建阶段（锁外）并发更新并 flush
            index.upsert("d1", "BRAND NEW VALUE token-zx")
            index.flush()

        t = idx.merge_background(during_build=during_build)
        t.join()

        snap = idx.open_snapshot()
        self.assertEqual(snap.get_doc("d1"), "BRAND NEW VALUE token-zx")
        self.assertIn("d1", snap.search("token-zx"))
        self.assertEqual(snap.count_visible(), 4)
        self.assertEqual(len(snap.segments), 2)  # 合并段 + 合并期间追加的新段
        snap.close()

    def test_concurrent_delete_during_merge_keeps_tombstone(self):
        idx = LsmTextIndex()
        for i in range(3):
            idx.upsert(f"d{i}", f"value {i}")
            idx.flush()

        def during_build(index: LsmTextIndex) -> None:
            index.delete("d2")
            index.flush()

        t = idx.merge_background(during_build=during_build)
        t.join()

        snap = idx.open_snapshot()
        self.assertIsNone(snap.get_doc("d2"))
        self.assertEqual(snap.count_visible(), 2)
        snap.close()


class TestEmptyAndEdge(unittest.TestCase):
    def test_empty_index(self):
        idx = LsmTextIndex()
        self.assertIsNone(idx.flush())
        self.assertIsNone(idx.merge())
        snap = idx.open_snapshot()
        self.assertEqual(snap.search("anything"), [])
        self.assertEqual(snap.count_visible(), 0)
        self.assertEqual(snap.stats("x")["visible_docs"], 0)
        snap.close()

    def test_unflushed_writes_not_visible(self):
        idx = LsmTextIndex()
        idx.upsert("a", "hello")
        snap = idx.open_snapshot()
        self.assertEqual(snap.count_visible(), 0)
        snap.close()


if __name__ == "__main__":
    unittest.main()
