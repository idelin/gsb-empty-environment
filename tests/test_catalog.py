import os
import random
import tempfile
import unittest

from interval_index import (
    CONTAINED_BY,
    CONTAINS,
    INTERSECTS,
    MISSING,
    Catalog,
    Interval,
    parse_document,
)

MODES = (INTERSECTS, CONTAINS, CONTAINED_BY)


def make_doc(doc_id, range_text):
    if range_text is None:
        header = f"id: {doc_id}\n"
    else:
        header = f"id: {doc_id}\nrange: {range_text}\n"
    return parse_document(header + f"\n正文 {doc_id}")


class CatalogCoreTest(unittest.TestCase):
    def setUp(self):
        self.cat = Catalog()
        self.cat.upsert_document(make_doc("a", "[0,5)"))   # [0,4]
        self.cat.upsert_document(make_doc("b", "[5,8)"))   # [5,7]
        self.cat.upsert_document(make_doc("c", "[3,6]"))   # [3,6]
        self.cat.upsert_document(make_doc("d", "[10,)"))   # [10,+inf)
        self.cat.upsert_document(make_doc("e", "(,0]"))    # (-inf,0]
        self.cat.upsert_document(make_doc("f", "(,)"))     # 全 Z
        self.cat.upsert_document(make_doc("g", None))      # 缺失
        self.cat.upsert_document(make_doc("h", "[7,7)"))   # 空区间
        self.cat.upsert_document(make_doc("p", "[5,5]"))   # 点区间

    def test_adjacent_half_open_disjoint(self):
        self.assertEqual(self.cat.query("[0,5)", INTERSECTS),
                         self.cat.full_scan("[0,5)", INTERSECTS))
        self.assertEqual(self.cat.query("[0,5)", INTERSECTS),
                         ["a", "c", "e", "f"])

    def test_modes_against_full_scan_fixed_queries(self):
        queries = ["[0,5)", "[5,8)", "[3,3]", "[5,5]", "(,4)",
                   "[9,12)", "(,0]", "[0,100]", "(,)", "[-1,1)",
                   "[6,6]", "(10,12)", "[11,11]"]
        for q in queries:
            for mode in MODES:
                with self.subTest(q=q, mode=mode):
                    self.assertEqual(self.cat.query(q, mode),
                                     self.cat.full_scan(q, mode))

    def test_point_interval_relations(self):
        self.assertEqual(self.cat.query("[5,5]", INTERSECTS),
                         ["b", "c", "f", "p"])
        self.assertEqual(self.cat.query("[5,5]", CONTAINS), ["p"])
        self.assertEqual(self.cat.query("[5,5]", CONTAINED_BY),
                         ["b", "c", "f", "p"])

    def test_missing_and_empty(self):
        self.assertEqual(self.cat.query(None, MISSING), ["g"])
        self.assertEqual(self.cat.empty_ids(), ["h"])
        for mode in MODES:
            self.assertEqual(self.cat.query("[0,100]", mode).count("g"), 0)
            self.assertEqual(self.cat.query("[0,100]", mode).count("h"), 0)

    def test_empty_or_missing_query_matches_nothing(self):
        for mode in MODES:
            self.assertEqual(self.cat.query("[5,5)", mode), [])
            self.assertEqual(self.cat.query("", mode), [])

    def test_no_duplicate_ids_even_with_overlapping_clauses(self):
        # 每篇文档在索引中只出现一次；结果本身是有序无重复列表。
        for q in ("(,)", "[0,10]", "[4,5]"):
            for mode in MODES:
                hits = self.cat.query(q, mode)
                self.assertEqual(len(hits), len(set(hits)))

    def test_update_deletes_old_index_entries(self):
        # 更新前 a 命中 [0,5)
        self.assertIn("a", self.cat.query("[0,5)", INTERSECTS))
        old_rows = self.cat.index_stats()

        self.cat.upsert_document(make_doc("a", "[100,200]"))
        # 新区间不再命中旧查询
        self.assertNotIn("a", self.cat.query("[0,5)", INTERSECTS))
        self.assertIn("a", self.cat.query("[150,150]", INTERSECTS))
        # 行数不增长：旧项已删除而非叠加
        new_rows = self.cat.index_stats()
        self.assertEqual(new_rows["lo_rows"], old_rows["lo_rows"])
        self.assertEqual(new_rows["hi_rows"], old_rows["hi_rows"])
        self.assertEqual(new_rows["documents"], old_rows["documents"])
        # 旧端点位置不再残留 a
        lo_keys = [(lo, hi) for lo, hi, doc_id in self.cat.lo_rows()]
        self.assertNotIn((0, 4), lo_keys)
        self.assertIn((100, 200), lo_keys)

    def test_update_interval_to_missing_and_back(self):
        self.assertIn("a", self.cat.query("(,)", INTERSECTS))
        self.cat.upsert_document(make_doc("a", None))
        self.assertNotIn("a", self.cat.query("(,)", INTERSECTS))
        self.assertEqual(self.cat.query(None, MISSING), ["a", "g"])
        self.cat.upsert_document(make_doc("a", "[1,2]"))
        self.assertIn("a", self.cat.query("[1,2]", INTERSECTS))
        self.assertEqual(self.cat.query(None, MISSING), ["g"])

    def test_update_empty_to_real(self):
        stats = self.cat.index_stats()
        self.cat.upsert_document(make_doc("h", "[7,7]"))
        self.assertIn("h", self.cat.query("[7,7]", INTERSECTS))
        self.assertEqual(self.cat.empty_ids(), [])
        self.assertEqual(self.cat.index_stats()["lo_rows"], stats["lo_rows"] + 1)

    def test_remove_document(self):
        self.assertTrue(self.cat.remove_document("a"))
        self.assertNotIn("a", self.cat.query("[0,5)", INTERSECTS))
        self.assertFalse(self.cat.remove_document("a"))
        self.assertEqual(len(self.cat), 8)

    def test_load_from_directory(self):
        with tempfile.TemporaryDirectory() as tmp:
            with open(os.path.join(tmp, "x.txt"), "w", encoding="utf-8") as fh:
                fh.write("id: x\nrange: [1,3)\n\n你好")
            with open(os.path.join(tmp, "ignore.log"), "w",
                      encoding="utf-8") as fh:
                fh.write("id: y\nrange: [1,3)\n")
            cat = Catalog.from_directory(tmp)
            self.assertIn("x", cat)
            self.assertNotIn("y", cat)


class RandomizedConsistencyTest(unittest.TestCase):
    """随机文档集合上索引结果必须始终等于全扫描结果。"""

    BOUNDS = [-50, -20, -7, -1, 0, 1, 3, 5, 8, 10, 20, 50]

    def _random_interval(self, rng):
        kind = rng.randrange(10)
        if kind == 0:
            return None  # 缺失
        if kind == 1:
            return "[7,7)"  # 空区间
        lo = rng.choice(self.BOUNDS)
        hi = rng.choice(self.BOUNDS)
        lbr = rng.choice("[](")
        rbr = rng.choice("])")
        # 偶尔制造无界端
        left = "" if rng.random() < 0.15 else str(lo)
        right = "" if rng.random() < 0.15 else str(hi)
        return f"{lbr}{left},{right}{rbr}"

    def test_random_upserts_queries_updates(self):
        rng = random.Random(20260926)
        cat = Catalog()
        active: dict[str, str | None] = {}

        for _ in range(400):
            doc_id = f"d{rng.randrange(60)}"
            spec = self._random_interval(rng)
            cat.upsert_document(make_doc(doc_id, spec))
            active[doc_id] = spec

        def expected(query_text, mode):
            q = Interval.parse(query_text)
            hits = []
            for doc_id, spec in active.items():
                iv = Interval.parse(spec)
                if iv.is_missing or iv.is_empty or q.is_missing or q.is_empty:
                    continue
                if mode == INTERSECTS:
                    ok = iv.lo <= q.hi and q.lo <= iv.hi
                elif mode == CONTAINS:
                    ok = q.lo <= iv.lo and iv.hi <= q.hi
                else:
                    ok = iv.lo <= q.lo and q.hi <= iv.hi
                if ok:
                    hits.append(doc_id)
            return sorted(hits)

        for _ in range(300):
            spec = self._random_interval(rng)
            if spec is None:
                continue
            for mode in MODES:
                got = cat.query(spec, mode)
                self.assertEqual(got, cat.full_scan(spec, mode),
                                 msg=(spec, mode))
                self.assertEqual(got, expected(spec, mode),
                                 msg=(spec, mode))
                self.assertEqual(len(got), len(set(got)))

        # 再做一批更新，验证旧项删除后一致性依旧
        for _ in range(200):
            doc_id = f"d{rng.randrange(60)}"
            spec = self._random_interval(rng)
            cat.upsert_document(make_doc(doc_id, spec))
            active[doc_id] = spec
        for spec in ("[0,5)", "[5,8)", "[5,5]", "(,)", "[-1,10]",
                     "(,0]", "[10,)", "[-100,-50]", "[6,9)"):
            for mode in MODES:
                self.assertEqual(cat.query(spec, mode), expected(spec, mode))


if __name__ == "__main__":
    unittest.main()
