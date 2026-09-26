import unittest

from fusion import Index, load_corpus, normalize_series, tokenize
from fusion.index import Paginator


class TestNormalize(unittest.TestCase):
    def test_minmax(self):
        self.assertEqual(normalize_series([1, 2, 3]), [0.0, 0.5, 1.0])

    def test_all_equal_no_division_by_zero(self):
        self.assertEqual(normalize_series([5, 5, 5]), [1.0, 1.0, 1.0])

    def test_single_value(self):
        self.assertEqual(normalize_series([7]), [1.0])

    def test_empty(self):
        self.assertEqual(normalize_series([]), [])


def make_index():
    return Index(load_corpus("data/corpus.tsv"))


class TestSnapshot(unittest.TestCase):
    def setUp(self):
        self.index = make_index()
        self.terms = tokenize("检索")

    def test_postings_sorted_deterministic(self):
        snap = self.index.get_snapshot(self.terms, "title")
        raws = [p.raw for p in snap.postings]
        self.assertEqual(raws, sorted(raws, reverse=True))

    def test_stats_are_explicit(self):
        snap = self.index.get_snapshot(self.terms, "title")
        self.assertEqual(snap.stats.min_raw, min(p.raw for p in snap.postings))
        self.assertEqual(snap.stats.max_raw, max(p.raw for p in snap.postings))
        self.assertEqual(snap.stats.doc_count, len(snap.postings))

    def test_snapshot_immutable_and_cached(self):
        a = self.index.get_snapshot(self.terms, "body")
        b = self.index.get_snapshot(self.terms, "body")
        self.assertIs(a, b)
        with self.assertRaises(AttributeError):
            a.postings = ()  # frozen tuple / dataclass

    def test_missing_doc_not_in_snapshot(self):
        snap = self.index.get_snapshot(self.terms, "title")
        ids = {p.doc_id for p in snap.postings}
        self.assertNotIn("d05", ids)  # 标题无命中

    def test_norm_in_unit_interval(self):
        for field in ("title", "body"):
            snap = self.index.get_snapshot(self.terms, field)
            for p in snap.postings:
                self.assertGreaterEqual(p.norm, 0.0)
                self.assertLessEqual(p.norm, 1.0)


class TestPaginator(unittest.TestCase):
    def setUp(self):
        self.index = make_index()

    def test_pagination_no_repeat_no_gap(self):
        snap = self.index.get_snapshot(tokenize("检索 融合"), "body")
        pg = Paginator(snap, page_size=2)
        seen = []
        while not pg.exhausted:
            seen.extend(p.doc_id for p in pg.fetch_page())
        self.assertEqual(seen, [p.doc_id for p in snap.postings])
        self.assertEqual(len(seen), len(set(seen)))

    def test_cap_is_head_norm_and_zero_after_exhausted(self):
        snap = self.index.get_snapshot(tokenize("检索"), "title")
        pg = Paginator(snap, page_size=1)
        self.assertEqual(pg.cap(), snap.postings[0].norm)
        while not pg.exhausted:
            pg.fetch_page()
        self.assertEqual(pg.cap(), 0.0)
        self.assertIsNone(pg.head())

    def test_cap_monotonic_nonincreasing(self):
        snap = self.index.get_snapshot(tokenize("检索 融合"), "body")
        pg = Paginator(snap, page_size=1)
        prev = pg.cap()
        while not pg.exhausted:
            pg.fetch_page()
            cur = pg.cap()
            self.assertLessEqual(cur, prev + 1e-12)
            prev = cur

    def test_page_size_must_be_positive(self):
        from fusion.index import FieldPlan
        snap = self.index.get_snapshot(tokenize("检索"), "title")
        with self.assertRaises(ValueError):
            FieldPlan(field="title", weight=1.0, snapshot=snap, page_size=0)


if __name__ == "__main__":
    unittest.main()
