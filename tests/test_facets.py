"""unittest suite for the bitmap facet engine.

The centrepiece is test_all_selection_combinations_match_reference:
every combination of facet selections (concrete values, MISSING, EMPTY,
OR-ed pairs) x several main queries is computed by the bitmap engine and
by the per-document boolean reference, and must agree exactly.
"""

from __future__ import annotations

import random
import unittest
from dataclasses import replace

from facetlib import (EMPTY, MISSING, Document, DocumentStore, FacetEngine,
                      TextQuery, reference_facets, tokenize)


def make_store(docs):
    store = DocumentStore()
    for d in docs:
        store.upsert(d)
    return store


# ---------------------------------------------------------------- corpus --
# D1: duplicate tag "phone" (must count once)
# D3: tags present but EMPTY
# D4: color MISSING
# D5: brand and color both MISSING, duplicate tag "food"
CORPUS = [
    Document("D1", "苹果 手机 Apple phone 5G",
             {"brand": ("apple",), "color": ("red",),
              "tags": ("phone", "5g", "phone")}),
    Document("D2", "苹果 平板 Apple tablet",
             {"brand": ("apple",), "color": ("blue",),
              "tags": ("tablet",)}),
    Document("D3", "三星 手机 Samsung phone",
             {"brand": ("samsung",), "color": ("red",), "tags": ()}),
    Document("D4", "三星 耳机 Samsung headset",
             {"brand": ("samsung",), "tags": ("audio",)}),
    Document("D5", "香蕉 水果 banana fruit",
             {"tags": ("food", "food")}),
]


def report_shape(report):
    """Everything except metrics, as a comparable tuple."""
    return (report.base_count, tuple(
        (d.dimension, d.context_size, tuple(sorted(d.counts.items())),
         d.missing, d.empty)
        for d in (report.dimensions[k] for k in sorted(report.dimensions))))


class TokenizerTest(unittest.TestCase):
    def test_rules(self):
        # case folding + latin/digit runs
        self.assertEqual(tokenize("Apple iPhone15"), ["apple", "iphone15"])
        # CJK ideographs are single-character tokens
        self.assertEqual(tokenize("苹果手机"), ["苹", "果", "手", "机"])
        # punctuation / whitespace are separators, digits kept
        self.assertEqual(tokenize("Wi-Fi 6, 蓝牙5.3!"),
                         ["wi", "fi", "6", "蓝", "牙", "5", "3"])
        # mixed scripts in one sentence
        self.assertEqual(tokenize("红色 red 红"), ["红", "色", "red", "红"])
        # casefold handles sharp-s etc.
        self.assertEqual(tokenize("STRASSE"), ["strasse"])


class FixedCorpusTest(unittest.TestCase):
    def setUp(self):
        self.store = make_store(CORPUS)
        self.snap = self.store.snapshot()

    def facets(self, query=None, selections=None):
        return FacetEngine(self.snap).compute(query, selections)

    def test_missing_and_empty_are_distinct(self):
        rep = self.facets()
        tags = rep.dimensions["tags"]
        self.assertEqual(tags.empty, 1)     # D3 has tags=()
        self.assertEqual(tags.missing, 0)   # every doc carries the key
        color = rep.dimensions["color"]
        self.assertEqual(color.missing, 2)  # D4, D5
        self.assertEqual(color.empty, 0)

    def test_duplicate_values_counted_once(self):
        rep = self.facets()
        tags = rep.dimensions["tags"].counts
        self.assertEqual(tags["phone"], 1)  # D1 lists it twice
        self.assertEqual(tags["food"], 1)   # D5 lists it twice

    def test_or_within_dimension(self):
        rep = self.facets(selections={"brand": ("apple", "samsung")})
        color = rep.dimensions["color"]
        # context = docs with brand apple OR samsung = D1..D4
        self.assertEqual(color.context_size, 4)
        self.assertEqual(color.counts, {"blue": 1, "red": 2})
        self.assertEqual(color.missing, 1)  # D4

    def test_and_across_dimensions(self):
        rep = self.facets(selections={"brand": ("apple",),
                                      "color": ("red",)})
        tags = rep.dimensions["tags"]
        # context = apple AND red = {D1} only
        self.assertEqual(tags.context_size, 1)
        self.assertEqual(tags.counts["phone"], 1)
        self.assertEqual(tags.counts["5g"], 1)
        self.assertEqual(tags.counts["tablet"], 0)

    def test_self_exclusion_with_two_selected_dimensions(self):
        rep = self.facets(query=TextQuery(all_of=("手机",)),
                          selections={"brand": ("apple",),
                                      "color": ("red",)})
        self.assertEqual(rep.base_count, 2)  # D1, D3
        brand = rep.dimensions["brand"]
        # brand counts see the color=red filter but NOT the brand filter
        self.assertEqual(brand.context_size, 2)
        self.assertEqual(brand.counts, {"apple": 1, "samsung": 1})
        color = rep.dimensions["color"]
        # color counts see the brand=apple filter but NOT the color filter
        self.assertEqual(color.context_size, 1)  # D1
        self.assertEqual(color.counts, {"blue": 0, "red": 1})

    def test_counts_never_include_non_query_docs(self):
        rep = self.facets(query=TextQuery(all_of=("手机",)))
        brand = rep.dimensions["brand"]
        self.assertEqual(rep.base_count, 2)
        self.assertEqual(brand.context_size, 2)
        self.assertEqual(sum(brand.counts.values())
                         + brand.missing + brand.empty, 2)
        self.assertEqual(brand.counts, {"apple": 1, "samsung": 1})

    def test_missing_and_empty_selectable(self):
        rep = self.facets(selections={"color": (MISSING,)})
        brand = rep.dimensions["brand"]
        # context = docs missing color = {D4, D5}
        self.assertEqual(brand.context_size, 2)
        self.assertEqual(brand.counts, {"apple": 0, "samsung": 1})
        self.assertEqual(brand.missing, 1)  # D5
        rep2 = self.facets(selections={"tags": (EMPTY,)})
        brand2 = rep2.dimensions["brand"]
        self.assertEqual(brand2.context_size, 1)  # D3
        self.assertEqual(brand2.counts, {"apple": 0, "samsung": 1})

    def test_unknown_dimension_selected_as_missing(self):
        rep = self.facets(selections={"nosuch": (MISSING,)})
        self.assertEqual(rep.base_count, 5)
        dim = rep.dimensions["nosuch"]
        self.assertEqual(dim.context_size, 5)
        self.assertEqual(dim.missing, 5)
        self.assertEqual(dim.empty, 0)
        self.assertEqual(dim.counts, {})

    def test_query_none_of_and_any_of(self):
        rep = self.facets(query=TextQuery(any_of=("apple", "三星"),
                                          none_of=("phone",)))
        # apple docs D1,D2 + 三星 docs D3,D4, minus phone docs D1,D3
        self.assertEqual(rep.base_count, 2)  # D2, D4
        brand = rep.dimensions["brand"]
        self.assertEqual(brand.counts, {"apple": 1, "samsung": 1})


class IncrementalAndSnapshotTest(unittest.TestCase):
    def setUp(self):
        self.store = make_store(CORPUS)
        self.v1 = self.store.snapshot()

    def mutate(self):
        self.store.upsert(Document("D2", "苹果 平板 Apple tablet",
                                   {"brand": ("apple",), "color": ("red",),
                                    "tags": ("tablet",)}))
        self.store.upsert(Document("D6", "苹果 手表 Apple watch",
                                   {"brand": ("apple",), "color": ("green",),
                                    "tags": ("watch",)}))
        self.store.delete("D4")
        return self.store.snapshot()

    def test_incremental_updates(self):
        v2 = self.mutate()
        rep = FacetEngine(v2).compute()
        color = rep.dimensions["color"]
        self.assertEqual(rep.base_count, 5)  # D1,D2,D3,D5,D6
        self.assertEqual(color.counts, {"green": 1, "red": 3})
        self.assertEqual(color.missing, 1)   # only D5 now
        brand = rep.dimensions["brand"]
        self.assertEqual(brand.counts, {"apple": 3, "samsung": 1})
        self.assertEqual(brand.missing, 1)

    def test_historical_snapshot_is_frozen(self):
        before = FacetEngine(self.v1).compute(
            TextQuery(all_of=("手机",)), {"brand": ("apple",)})
        v2 = self.mutate()
        after = FacetEngine(self.v1).compute(
            TextQuery(all_of=("手机",)), {"brand": ("apple",)})
        self.assertEqual(report_shape(before), report_shape(after))
        # the new snapshot genuinely differs
        now = FacetEngine(v2).compute(
            TextQuery(all_of=("手机",)), {"brand": ("apple",)})
        self.assertNotEqual(report_shape(before), report_shape(now))
        self.assertEqual(before.dimensions["brand"].counts,
                         {"apple": 1, "samsung": 1})

    def test_delete_then_reinsert_identity(self):
        self.store.delete("D1")
        self.store.upsert(CORPUS[0])
        rep = FacetEngine(self.store.snapshot()).compute()
        self.assertEqual(rep.dimensions["tags"].counts["phone"], 1)


class MetricsTest(unittest.TestCase):
    def setUp(self):
        self.snap = make_store(CORPUS).snapshot()

    def test_no_document_rescan_and_bitmap_reuse(self):
        engine = FacetEngine(self.snap)
        sel = {"brand": ("apple",), "color": ("red",)}
        rep1 = engine.compute(TextQuery(all_of=("手机",)), sel)
        m1 = rep1.metrics
        self.assertEqual(m1.docs_examined, 0)       # never touches docs
        self.assertGreater(m1.bitmap_lookups, 0)
        self.assertGreater(m1.cache_hits, 0)        # bitmaps were reused
        # second identical run: every bitmap comes from cache
        rep2 = engine.compute(TextQuery(all_of=("手机",)), sel)
        m2 = rep2.metrics
        self.assertEqual(m2.bitmap_lookups, m1.bitmap_lookups)
        self.assertGreater(m2.cache_hits, m1.cache_hits)
        self.assertEqual(report_shape(rep1), report_shape(rep2))


class ReferenceCrossCheckTest(unittest.TestCase):
    """Exhaustive selections x queries: bitmap engine == boolean oracle."""

    @classmethod
    def setUpClass(cls):
        rng = random.Random(20260926)
        brands = ["apple", "samsung", "sony"]
        colors = ["red", "green", "blue"]
        tags = ["phone", "5g", "audio", "food"]
        vocab = ["苹果", "三星", "手机", "耳机", "平板",
                 "apple", "phone", "pro", "x1"]
        docs = []
        for i in range(40):
            attrs = {}
            r = rng.random()
            if r < 0.65:
                attrs["brand"] = tuple(rng.sample(brands, rng.randint(1, 2)))
            elif r < 0.75:
                attrs["brand"] = ()
            r = rng.random()
            if r < 0.65:
                attrs["color"] = (rng.choice(colors),)
            elif r < 0.75:
                attrs["color"] = ()
            r = rng.random()
            if r < 0.65:  # duplicates on purpose
                attrs["tags"] = tuple(rng.choice(tags)
                                      for _ in range(rng.randint(1, 4)))
            elif r < 0.75:
                attrs["tags"] = ()
            text = " ".join(rng.choice(vocab)
                            for _ in range(rng.randint(3, 8)))
            docs.append(Document(f"R{i:02d}", text, attrs))
        # guarantee edge cases exist
        docs.append(Document("R40", "手机", {"brand": (), "color": (),
                                             "tags": ()}))
        docs.append(Document("R41", "手机", {}))
        docs.append(Document("R42", "手机",
                             {"brand": ("apple", "apple"),
                              "color": ("red", "red")}))
        cls.docs = docs
        cls.snap = make_store(docs).snapshot()

    def test_all_selection_combinations_match_reference(self):
        option_sets = {
            "brand": [(), ("apple",), ("sony",), ("apple", "samsung"),
                      (MISSING,), (EMPTY,), (MISSING, "apple")],
            "color": [(), ("red",), ("blue",), ("red", "green"),
                      (MISSING,), (EMPTY,), (EMPTY, "red")],
            "tags": [(), ("phone",), ("5g",), ("phone", "audio"),
                     (MISSING,), (EMPTY,), (MISSING, EMPTY)],
        }
        queries = [
            TextQuery(),
            TextQuery(all_of=("手机",)),
            TextQuery(any_of=("apple", "三星")),
            TextQuery(all_of=("手机",), none_of=("phone",)),
        ]
        checked = 0
        for query in queries:
            for b in option_sets["brand"]:
                for c in option_sets["color"]:
                    for t in option_sets["tags"]:
                        sel = {"brand": b, "color": c, "tags": t}
                        got = FacetEngine(self.snap).compute(query, sel)
                        want = reference_facets(self.snap.docs.values(),
                                                query, sel)
                        self.assertEqual(
                            report_shape(got), report_shape(want),
                            msg=f"query={query} selections={sel}")
                        checked += 1
        self.assertEqual(checked, 4 * 7 ** 3)

    def test_base_bitmap_doc_identity_matches_reference(self):
        queries = [
            TextQuery(),
            TextQuery(all_of=("手机",)),
            TextQuery(any_of=("apple", "三星"), none_of=("pro",)),
            TextQuery(all_of=("手机", "apple")),
        ]
        engine = FacetEngine(self.snap)
        for query in queries:
            got = set(self.snap.doc_ids_in(engine.base_bitmap(query)))
            want = {d.doc_id for d in self.snap.docs.values()
                    if query in [query] and _matches(d, query)}
            self.assertEqual(got, want, msg=f"query={query}")

    def test_reference_reports_its_document_accesses(self):
        rep = reference_facets(self.snap.docs.values(),
                               TextQuery(all_of=("手机",)),
                               {"brand": ("apple",)})
        self.assertGreater(rep.metrics.docs_examined, 0)
        engine_rep = FacetEngine(self.snap).compute(
            TextQuery(all_of=("手机",)), {"brand": ("apple",)})
        self.assertEqual(engine_rep.metrics.docs_examined, 0)


def _matches(doc, query):
    from facetlib.reference import query_matches
    from facetlib import token_set
    return query_matches(token_set(doc.text), query)


if __name__ == "__main__":
    unittest.main()
