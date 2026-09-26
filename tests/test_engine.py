import unittest

from paginated_search import (
    CursorError,
    CursorInvalidated,
    CursorQueryMismatch,
    SearchEngine,
    tokenize,
)


def collect_all(engine, query, page_size):
    """翻完全部页，返回 (doc_id 列表, 页数)。"""
    ids, cursor, pages = [], None, 0
    while True:
        page = engine.search(query, page_size=page_size, cursor=cursor)
        ids.extend(hit.doc_id for hit in page.items)
        pages += 1
        cursor = page.next_cursor
        if cursor is None:
            return ids, pages


DOCS = [
    ("d1", "Python 分页 分页 游标"),
    ("d2", "Python 分页 索引"),
    ("d3", "Python 分页 快照"),
    ("d4", "Go 语言 分页"),
    ("d5", "Python 分页 版本"),
]


class TokenizerTest(unittest.TestCase):
    def test_ascii_alnum_runs(self):
        self.assertEqual(tokenize("Python 3.11"), ["python", "3", "11"])

    def test_casefold_and_nfkc(self):
        self.assertEqual(tokenize("ＰＹＴＨＯＮ Café"), ["python", "café"])

    def test_cjk_unigram_and_bigram(self):
        self.assertEqual(tokenize("分页"), ["分", "页", "分页"])

    def test_mixed_text(self):
        self.assertEqual(
            tokenize("固定索引版本"),
            ["固", "定", "固定", "索", "定索", "引", "索引", "版", "引版", "本", "版本"],
        )

    def test_punctuation_is_separator(self):
        self.assertEqual(tokenize("hello,world!"), ["hello", "world"])


class PaginationTest(unittest.TestCase):
    def setUp(self):
        self.engine = SearchEngine()
        self.version = self.engine.update(DOCS)

    def test_total_order_tie_break_by_doc_id(self):
        # d2/d3/d5 同分（各含一次「分页」相关词元），必须按 ID 升序
        ids, _ = collect_all(self.engine, "python 分页", page_size=1)
        self.assertEqual(ids, ["d1", "d2", "d3", "d5"])  # d1 评分最高

    def test_no_duplicate_no_missing_across_pages(self):
        ids, pages = collect_all(self.engine, "分页", page_size=2)
        self.assertEqual(pages, 3)  # 5 条命中，2+2+1
        self.assertEqual(len(ids), len(set(ids)))
        self.assertEqual(set(ids), {"d1", "d2", "d3", "d4", "d5"})

    def test_page_size_change_does_not_repeat(self):
        # 先用页大小 2 读一页，再改用 3、1 续读，不允许重复或漏项
        seen = []
        page = self.engine.search("分页", page_size=2)
        seen.extend(h.doc_id for h in page.items)
        page = self.engine.search("分页", page_size=3, cursor=page.next_cursor)
        seen.extend(h.doc_id for h in page.items)
        self.assertIsNone(page.next_cursor)
        self.assertEqual(len(seen), len(set(seen)))
        self.assertEqual(set(seen), {"d1", "d2", "d3", "d4", "d5"})

    def test_cursor_rejected_for_different_query(self):
        page = self.engine.search("分页", page_size=2)
        with self.assertRaises(CursorQueryMismatch):
            self.engine.search("索引", page_size=2, cursor=page.next_cursor)

    def test_cursor_rejected_for_equivalent_query_reworded(self):
        page = self.engine.search("python 分页", page_size=2)
        with self.assertRaises(CursorQueryMismatch):
            self.engine.search("分页 快照", page_size=2, cursor=page.next_cursor)

    def test_malformed_cursor(self):
        with self.assertRaises(CursorError):
            self.engine.search("分页", cursor="not-a-cursor")

    def test_empty_result_has_no_cursor(self):
        page = self.engine.search("不存在的词xyz")
        self.assertEqual(page.items, ())
        self.assertIsNone(page.next_cursor)


class VersioningTest(unittest.TestCase):
    def test_snapshot_retained_after_update(self):
        engine = SearchEngine()
        v1 = engine.update(DOCS)
        page = engine.search("分页", page_size=2)
        self.assertEqual(page.index_version, v1)

        v2 = engine.update(DOCS + [("d6", "Python 分页 新增")])  # 默认保留快照
        self.assertNotEqual(v1, v2)

        # 旧游标继续在 v1 快照上翻页，看不到新增的 d6
        rest, cursor = [], page.next_cursor
        while cursor is not None:
            p = engine.search("分页", page_size=2, cursor=cursor)
            self.assertEqual(p.index_version, v1)
            rest.extend(h.doc_id for h in p.items)
            cursor = p.next_cursor
        self.assertNotIn("d6", rest)
        self.assertEqual(set(rest), {"d1", "d2", "d3", "d4", "d5"} - {"d1", "d2"})

        # 新查询走新版本，能看到 d6
        ids, _ = collect_all(engine, "分页", page_size=10)
        self.assertIn("d6", ids)

    def test_explicit_invalidation_rejects_old_cursor(self):
        engine = SearchEngine()
        engine.update(DOCS)
        page = engine.search("分页", page_size=2)
        engine.update(DOCS + [("d6", "Python 分页 新增")], invalidate_previous=True)
        with self.assertRaises(CursorInvalidated):
            engine.search("分页", page_size=2, cursor=page.next_cursor)

    def test_invalidate_specific_version(self):
        engine = SearchEngine()
        v1 = engine.update(DOCS)
        page = engine.search("分页", page_size=2)
        engine.update(DOCS)  # v1 进入快照
        engine.invalidate(v1)
        with self.assertRaises(CursorInvalidated):
            engine.search("分页", page_size=2, cursor=page.next_cursor)

    def test_duplicate_doc_id_rejected(self):
        engine = SearchEngine()
        with self.assertRaises(ValueError):
            engine.update([("d1", "a"), ("d1", "b")])


if __name__ == "__main__":
    unittest.main()
