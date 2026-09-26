import unittest

from search_engine import (
    CursorIndexExpiredError,
    CursorQueryMismatchError,
    CursorTamperedError,
    SearchEngine,
    UpdatePolicy,
)

DOCS = {
    "d01": "苹果 苹果 手机",
    "d02": "苹果手机",
    "d03": "苹果 香蕉 苹果",
    "d04": "手机 手机 手机",
    "d05": "苹果",
    "d06": "香蕉 苹果 手机 苹果",
    "d07": "完全不相关的文档",
}


def make_engine(policy=UpdatePolicy.INVALIDATE, docs=DOCS):
    engine = SearchEngine(on_update=policy)
    for doc_id, text in docs.items():
        engine.add_document(doc_id, text)
    return engine


def collect_all(engine, query, page_size):
    """翻页遍历全部结果，返回 (doc_id 列表, 页数)。"""
    seen, cursor, pages = [], None, 0
    while True:
        page = engine.search(query, page_size=page_size, cursor=cursor)
        seen.extend(hit.doc_id for hit in page.hits)
        pages += 1
        if page.next_cursor is None:
            return seen, pages
        cursor = page.next_cursor


class RankingTest(unittest.TestCase):
    def test_and_semantics_and_tf_score(self):
        engine = make_engine()
        page = engine.search("苹果 手机", page_size=10)
        # 同时含“苹/果/手/机”的文档：d01(2+2+1+1=6)、d06(2+2+1+1=6)、d02(1+1+1+1=4)
        self.assertEqual(
            [(h.doc_id, h.score) for h in page.hits],
            [("d01", 6), ("d06", 6), ("d02", 4)],
        )

    def test_ties_form_total_order_by_doc_id(self):
        engine = make_engine()
        page = engine.search("苹果", page_size=10)
        scores = [h.score for h in page.hits]
        self.assertEqual(scores, sorted(scores, reverse=True))
        by_score = {}
        for hit in page.hits:
            by_score.setdefault(hit.score, []).append(hit.doc_id)
        for ids in by_score.values():
            self.assertEqual(ids, sorted(ids))  # 同分按 ID 字典序


class PaginationTest(unittest.TestCase):
    def test_no_duplicate_no_missing_across_pages(self):
        engine = make_engine()
        expected = [h.doc_id for h in engine.search("苹果", page_size=100).hits]
        for page_size in (1, 2, 3, 7):
            seen, pages = collect_all(engine, "苹果", page_size)
            self.assertEqual(seen, expected)          # 顺序一致 => 不漏项
            self.assertEqual(len(seen), len(set(seen)))  # 不重复
            self.assertEqual(pages, -(-len(expected) // page_size))

    def test_page_size_change_midstream_no_reread(self):
        engine = make_engine()
        page1 = engine.search("苹果", page_size=1)
        page2 = engine.search("苹果", page_size=3, cursor=page1.next_cursor)
        page3 = engine.search("苹果", page_size=4, cursor=page2.next_cursor)
        seen = [h.doc_id for h in (*page1.hits, *page2.hits, *page3.hits)]
        expected = [h.doc_id for h in engine.search("苹果", page_size=100).hits]
        self.assertEqual(seen, expected)
        self.assertIsNone(page3.next_cursor)

    def test_cursor_rejected_for_different_query(self):
        engine = make_engine()
        cursor = engine.search("苹果", page_size=2).next_cursor
        with self.assertRaises(CursorQueryMismatchError):
            engine.search("手机", page_size=2, cursor=cursor)
        with self.assertRaises(CursorQueryMismatchError):
            engine.search("苹果 手机", page_size=2, cursor=cursor)

    def test_cursor_allows_equivalent_query_surface_form(self):
        engine = make_engine()
        cursor = engine.search("苹果 手机", page_size=1).next_cursor
        # 大小写/标点不同但分词结果相同的查询视为同一查询
        page = engine.search("苹果，手机", page_size=1, cursor=cursor)
        self.assertEqual(len(page.hits), 1)

    def test_tampered_cursor_rejected(self):
        engine = make_engine()
        cursor = engine.search("苹果", page_size=2).next_cursor
        with self.assertRaises(CursorTamperedError):
            engine.search("苹果", page_size=2, cursor=cursor[:-1] + "0")
        with self.assertRaises(CursorTamperedError):
            engine.search("苹果", page_size=2, cursor="not-a-cursor")

    def test_invalid_arguments(self):
        engine = make_engine()
        with self.assertRaises(ValueError):
            engine.search("苹果", page_size=0)
        with self.assertRaises(ValueError):
            engine.search("！！！")


class UpdatePolicyTest(unittest.TestCase):
    def test_invalidate_policy_expires_old_cursor(self):
        engine = make_engine(UpdatePolicy.INVALIDATE)
        cursor = engine.search("苹果", page_size=2).next_cursor
        engine.add_document("d08", "苹果 新文档")
        with self.assertRaises(CursorIndexExpiredError):
            engine.search("苹果", page_size=2, cursor=cursor)

    def test_snapshot_policy_keeps_old_cursor_readable(self):
        engine = make_engine(UpdatePolicy.SNAPSHOT)
        page1 = engine.search("苹果", page_size=3)
        old_version = page1.index_version
        engine.add_document("d08", "苹果 苹果 苹果 新文档")
        # 旧游标继续翻页，读到的仍是旧版本快照
        page2 = engine.search("苹果", page_size=3, cursor=page1.next_cursor)
        self.assertEqual(page2.index_version, old_version)
        seen = [h.doc_id for h in (*page1.hits, *page2.hits)]
        self.assertNotIn("d08", seen)
        self.assertEqual(len(seen), len(set(seen)))
        # 新查询使用最新版本，能看到新文档且它排名第一
        fresh = engine.search("苹果", page_size=10)
        self.assertGreater(fresh.index_version, old_version)
        self.assertEqual(fresh.hits[0].doc_id, "d08")

    def test_remove_document_bumps_version(self):
        engine = make_engine(UpdatePolicy.SNAPSHOT)
        version_before = engine.version
        engine.remove_document("d07")
        self.assertEqual(engine.version, version_before + 1)
        ids = [h.doc_id for h in engine.search("苹果", page_size=100).hits]
        self.assertNotIn("d07", ids)


class Utf8EndToEndTest(unittest.TestCase):
    def test_chinese_full_flow(self):
        engine = SearchEngine()
        engine.add_document("甲", "搜索引擎 使用 倒排索引")
        engine.add_document("乙", "搜索引擎 搜索引擎 分页")
        engine.add_document("丙", "数据库 分页")
        seen, _ = collect_all(engine, "搜索引擎", 1)
        self.assertEqual(seen, ["乙", "甲"])  # 乙词频更高；同分才按 ID


if __name__ == "__main__":
    unittest.main()
