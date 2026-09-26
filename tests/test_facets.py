"""facetlib 单元测试（Python 3.11 标准库 unittest）。

运行：python3.11 -m unittest discover -s tests -v
"""

from __future__ import annotations

import unittest

from facetlib import (
    MISSING,
    Doc,
    Query,
    build_index,
    evaluate,
    parse_doc,
    render_doc,
    tokenize,
    verify_all_combinations,
)


def make_docs():
    return [
        Doc("d1", "苹果 手机 alpha", {"颜色": ("红", "红"), "作者": ("alice",), "年份": ("2024",), "来源": ("web",)}),
        Doc("d2", "香蕉 手机", {"颜色": ("绿",), "作者": ("bob",), "来源": ("app",)}),  # 年份缺失
        Doc("d3", "苹果 电脑 beta", {"颜色": (), "作者": ("alice",), "年份": ("2024",), "来源": ("web",)}),  # 颜色空集合
        Doc("d4", "橘子 平板", {"作者": ("carol",), "年份": ("2025",), "来源": ("app",)}),  # 颜色缺失
        Doc("d5", "苹果 香蕉 手机", {"颜色": ("红", "蓝"), "作者": ("bob",), "年份": ("2024",), "来源": ("web",)}),
        Doc("d6", "alpha beta gamma", {"颜色": ("蓝",), "年份": ("2023",), "来源": ("app",)}),  # 作者缺失
    ]


class TokenizerTest(unittest.TestCase):
    def test_rules(self):
        self.assertEqual(tokenize("Hello, 世界! foo_bar2 ＡＢ"),
                         ["hello", "世", "界", "foo_bar2", "ab"])
        self.assertEqual(tokenize("苹果"), ["苹", "果"])  # CJK 单字词元
        self.assertEqual(tokenize(""), [])


class DocFormatTest(unittest.TestCase):
    def test_roundtrip_preserves_missing_and_empty(self):
        doc = Doc("x1", "正文", {"颜色": (), "作者": ("alice",)})  # 年份缺失
        parsed = parse_doc(render_doc(doc))
        self.assertEqual(parsed.facets, doc.facets)
        self.assertNotIn("年份", parsed.facets)  # 缺失
        self.assertEqual(parsed.facets["颜色"], ())  # 空集合 ≠ 缺失


class FacetEngineTest(unittest.TestCase):
    def setUp(self):
        self.docs = make_docs()
        self.index = build_index(self.docs)

    def test_or_within_facet_and_across_facets(self):
        q = Query(terms=("苹", "果"),
                  filters={"颜色": frozenset({"红", "绿"}), "作者": frozenset({"alice"})})
        res, _ = evaluate(self.index, q)
        self.assertEqual(res["来源"].base_size, 1)  # main AND (红∨绿) AND alice -> d1
        self.assertEqual(res["来源"].counts, {"web": 1})
        self.assertEqual(res["颜色"].base_size, 2)  # 自排除: main AND alice -> d1,d3
        self.assertEqual(res["作者"].counts, {"alice": 1, "bob": 1})  # main AND (红∨绿)
        self.assertEqual(res["年份"].counts, {"2024": 1})  # 含全部分面过滤 -> d1

    def test_self_exclusion_ignores_own_filter(self):
        q = Query(terms=("苹", "果"), filters={"颜色": frozenset({"红"})})
        res, _ = evaluate(self.index, q)
        self.assertEqual(res["颜色"].base_size, 3)  # d1,d3,d5：不含颜色过滤
        self.assertEqual(res["颜色"].counts, {"红": 2, "蓝": 1})
        res_off, _ = evaluate(self.index, q, self_exclude=False)
        self.assertEqual(res_off["颜色"].base_size, 2)  # 非自排除: d1,d5

    def test_two_facets_selected_scope_is_explicit(self):
        q = Query(terms=("苹", "果"),
                  filters={"颜色": frozenset({"红"}), "作者": frozenset({"alice"})})
        res, _ = evaluate(self.index, q)
        self.assertEqual(res["颜色"].base_size, 2)  # 含作者过滤、排除自身 -> d1,d3
        self.assertEqual(res["作者"].base_size, 2)  # 含颜色过滤、排除自身 -> d1,d5
        self.assertEqual(res["作者"].counts, {"alice": 1, "bob": 1})
        self.assertEqual(res["年份"].base_size, 1)  # 含两者过滤 -> d1

    def test_missing_single_bucket_and_empty_set_distinct(self):
        res, _ = evaluate(self.index, Query())
        self.assertEqual(res["颜色"].counts, {"红": 2, "绿": 1, "蓝": 2})
        self.assertEqual(res["颜色"].missing, 1)  # 仅 d4；d3 是空集合而非缺失
        self.assertEqual(res["颜色"].base_size, 6)
        q = Query(filters={"颜色": frozenset({MISSING})})
        res, _ = evaluate(self.index, q)
        self.assertEqual(res["作者"].base_size, 1)  # 只命中 d4，空集合的 d3 不命中
        self.assertEqual(res["作者"].counts, {"carol": 1})
        q_empty = Query(filters={"颜色": frozenset()})  # 空选择 = 不约束
        res, _ = evaluate(self.index, q_empty)
        self.assertEqual(res["作者"].base_size, 6)

    def test_duplicate_tags_counted_once_per_doc(self):
        res, _ = evaluate(self.index, Query(terms=("苹", "果")))
        self.assertEqual(res["颜色"].counts["红"], 2)  # d1 的 (红,红) 只计一次

    def test_counts_exclude_non_matching_main_query(self):
        res, _ = evaluate(self.index, Query(terms=("苹", "果")))
        self.assertEqual(res["颜色"].base_size, 3)
        self.assertEqual(res["颜色"].counts.get("蓝", 0), 1)  # d6 有蓝但不匹配主查询
        self.assertEqual(res["来源"].counts.get("app", 0), 0)  # d2/d4/d6 均被主查询排除

    def test_incremental_update_by_doc_identity(self):
        q = Query(terms=("苹", "果"))
        self.index.remove("d5")
        res, _ = evaluate(self.index, q)
        self.assertEqual(res["颜色"].counts.get("红", 0), 1)
        self.index.add_or_update(Doc("d2", "香蕉 手机",
                                     {"颜色": ("绿",), "作者": ("bob",), "年份": ("2024",)}))
        res, _ = evaluate(self.index, Query())
        self.assertEqual(res["年份"].missing, 0)  # d2 更新后不再缺失年份
        self.index.add_or_update(Doc("d7", "苹果 手表", {"颜色": ("绿",), "作者": ("alice",)}))
        res, _ = evaluate(self.index, q)
        self.assertEqual(res["颜色"].counts.get("绿", 0), 1)
        self.assertEqual(res["颜色"].base_size, 3)  # d1,d3,d7
        res, _ = evaluate(self.index, Query())
        self.assertEqual(res["年份"].missing, 1)  # d7 无年份

    def test_snapshot_isolated_from_updates(self):
        q = Query(terms=("苹", "果"))
        snap = self.index.snapshot()
        before = evaluate(snap, q)[0]["颜色"].counts["红"]
        self.index.remove("d5")
        self.index.add_or_update(Doc("d8", "苹果", {"颜色": ("红",)}))
        self.assertEqual(evaluate(snap, q)[0]["颜色"].counts["红"], before)  # 历史快照不变
        self.assertEqual(evaluate(self.index, q)[0]["颜色"].counts["红"], 2)  # d1,d8
        self.assertLess(snap.version, self.index.version)

    def test_bitmap_sharing_and_no_rescan(self):
        q = Query(terms=("苹", "果"), filters={
            "颜色": frozenset({"红"}), "作者": frozenset({"alice"}),
            "年份": frozenset({"2024"}), "来源": frozenset({"web"}),
        })
        _, stats = evaluate(self.index, q)
        self.assertEqual(stats.main_bitmap_built, 1)  # 主查询位图只建一次
        self.assertEqual(stats.filter_bitmaps_built, 4)  # 每个过滤分面只建一次
        self.assertEqual(stats.conjunction_ands, 8)  # 前缀+后缀 = 2k，而非 k(k-1)=12
        self.assertEqual(stats.docs_scanned, 0)  # 不逐文档重扫

    def test_exhaustive_all_selection_combinations(self):
        domains = {
            "颜色": ("红", "绿", "蓝"),
            "作者": ("alice", "bob"),
            "年份": ("2024", "2025"),
            "来源": ("web", "app"),
        }
        combos, checks, mismatches = verify_all_combinations(
            self.docs, self.index, domains, term_queries=[(), ("苹", "果")])
        self.assertEqual(combos, 2 * 16 * 8 * 8 * 8)  # 2 组主查询 × 各分面选择子集数
        self.assertEqual(mismatches, [])
        self.assertGreater(checks, 0)


if __name__ == "__main__":
    unittest.main()
