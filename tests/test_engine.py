import random
import unittest

from fusion.engine import Engine, SnapshotNotFoundError
from fusion.fusion import NegativeWeightError, UnknownFieldError
from fusion.index import Document, FieldQueryView, Index

FIELDS = ["title", "body", "notes"]


def tiny_index():
    docs = [
        Document("d1", {"title": "x x", "body": "y"}),           # no notes
        Document("d2", {"title": "x", "body": "x x", "notes": "z"}),
        Document("d3", {"title": "y", "body": "x", "notes": "x"}),
    ]
    return Index(docs, FIELDS)


def random_index(rng, n_docs=40):
    vocab = ["机", "器", "学", "习", "machine", "learning", "python", "数据", "模型"]
    docs = []
    for i in range(n_docs):
        fields = {}
        for name in FIELDS:
            if rng.random() < 0.8:  # 20% of fields are missing
                fields[name] = " ".join(
                    rng.choice(vocab) for _ in range(rng.randint(3, 12))
                )
        if not fields:
            fields = {"title": "空"}
        docs.append(Document(f"doc{i:03d}", fields))
    return Index(docs, FIELDS)


class ValidationTest(unittest.TestCase):
    def setUp(self):
        self.engine = Engine(tiny_index())

    def test_unknown_field_rejected(self):
        with self.assertRaises(UnknownFieldError):
            self.engine.search("x", {"nope": 1.0}, k=3)

    def test_nan_weight_rejected(self):
        with self.assertRaises(ValueError):
            self.engine.search("x", {"title": float("nan")}, k=3)

    def test_empty_weights_rejected(self):
        with self.assertRaises(ValueError):
            self.engine.search("x", {}, k=3)

    def test_all_zero_weights_empty_ranking(self):
        result = self.engine.search("x", {"title": 0.0, "body": 0.0}, k=3)
        self.assertEqual(result.items, [])
        self.assertEqual(result.meta["mode"], "none")


class ZeroWeightTest(unittest.TestCase):
    def test_zero_weight_field_does_not_affect_ranking(self):
        engine = Engine(tiny_index())
        plain = engine.search("x", {"title": 1.0, "body": 1.0}, k=5)
        with_zero = engine.search(
            "x", {"title": 1.0, "body": 1.0, "notes": 0.0}, k=5
        )
        self.assertEqual(plain.items, with_zero.items)
        # The zero-weight field is never even accessed.
        self.assertEqual(with_zero.meta["fields"], ["body", "title"])
        self.assertNotIn("notes", with_zero.meta["stats"])


class NegativeWeightTest(unittest.TestCase):
    def test_strict_mode_rejects(self):
        engine = Engine(tiny_index())
        with self.assertRaises(NegativeWeightError):
            engine.search("x", {"title": 1.0, "body": -0.5}, k=3)

    def test_degrade_mode_falls_back_to_exhaustive(self):
        index = tiny_index()
        engine = Engine(index, on_negative_weights="degrade")
        weights = {"title": 1.0, "body": -0.5}
        result = engine.search("x", weights, k=3)
        self.assertTrue(result.meta["degraded"])
        self.assertEqual(result.meta["mode"], "exhaustive")
        self.assertIn("negative weights", result.meta["reason"])
        # Matches the exhaustive reference with signed weights exactly.
        reference = engine.exhaustive("x", weights, k=3)
        self.assertEqual(
            [(s.doc_id, s.score) for s in result.items],
            [(s.doc_id, s.score) for s in reference.items],
        )
        # Hand-computed expectations on the tiny fixture (query "x"):
        # title stats (0,2); body stats (0,2); notes is zero-weight here.
        # d1: title raw2 -> 1.0, body raw0 -> present -> 0.0
        #     score = (1*1.0 - 0.5*0.0) / 0.5 = 2.0
        # d2: title 0.5, body 1.0 -> (0.5 - 0.5) / 0.5 = 0.0
        # d3: title 0.0, body raw1 -> 0.5 -> (0.0 - 0.25) / 0.5 = -0.5
        got = {sd.doc_id: sd.score for sd in result.items}
        self.assertAlmostEqual(got["d1"], 2.0)
        self.assertAlmostEqual(got["d2"], 0.0)
        self.assertAlmostEqual(got["d3"], -0.5)

    def test_degrade_drops_nonpositive_denominator(self):
        index = tiny_index()
        engine = Engine(index, on_negative_weights="degrade")
        # title 0.5, body -1.0: d1 has both -> denominator -0.5 -> dropped.
        result = engine.search("x", {"title": 0.5, "body": -1.0}, k=10)
        ids = [sd.doc_id for sd in result.items]
        self.assertNotIn("d1", ids)


class CorruptView:
    """Wraps a view and breaks the non-increasing sorted-access assumption."""

    def __init__(self, inner):
        self._inner = inner
        self.field = inner.field
        self.stats = inner.stats

    @property
    def has_field(self):
        return self._inner.has_field

    def sorted_access(self):
        items = list(self._inner.sorted_access())
        yield from reversed(items)

    def random_access(self, doc_id):
        return self._inner.random_access(doc_id)


class CorruptEngine(Engine):
    def _build_views(self, query, fields):
        views = super()._build_views(query, fields)
        return {f: CorruptView(v) for f, v in views.items()}


class DegradationTest(unittest.TestCase):
    def test_non_monotone_stream_degrades_to_exhaustive(self):
        docs = [
            Document("a", {"title": "x x x", "body": "x"}),
            Document("b", {"title": "x x", "body": "y"}),
            Document("c", {"title": "x", "body": "y"}),
        ]
        index = Index(docs, FIELDS)
        engine = CorruptEngine(index)
        weights = {"title": 1.0, "body": 1.0}
        result = engine.search("x", weights, k=2)
        self.assertTrue(result.meta["degraded"])
        self.assertEqual(result.meta["mode"], "exhaustive")
        self.assertIn("upper-bound assumption violated", result.meta["reason"])
        reference = Engine(index).exhaustive("x", weights, k=2)
        self.assertEqual(
            [(s.doc_id, s.score) for s in result.items],
            [(s.doc_id, s.score) for s in reference.items],
        )


class RandomizedAgreementTest(unittest.TestCase):
    def test_ta_matches_exhaustive_on_random_corpora(self):
        rng = random.Random(20260926)
        for trial in range(10):
            index = random_index(random.Random(rng.randint(0, 10**9)))
            engine = Engine(index)
            weights = {}
            while not any(w > 0 for w in weights.values()):
                weights = {
                    f: rng.choice([0.0, 0.5, 1.0, 2.0]) for f in FIELDS
                }
            query = " ".join(
                rng.choice(["机", "器", "学习", "machine", "python"])
                for _ in range(rng.randint(1, 3))
            )
            for k in (1, 3, 7, 100):
                got = engine.search(query, weights, k=k)
                want = engine.exhaustive(query, weights, k=k)
                self.assertEqual(
                    [(s.doc_id, s.score) for s in got.items],
                    [(s.doc_id, s.score) for s in want.items],
                    f"trial={trial} k={k} query={query!r} weights={weights}",
                )
                report = engine.verify(query, weights, k=k)
                self.assertTrue(report["ok"], report)

    def test_early_stop_actually_happens(self):
        docs = [Document("star", {"title": "x " * 50, "body": "x " * 50})]
        for i in range(30):
            docs.append(Document(f"n{i}", {"title": "x", "body": "x"}))
        engine = Engine(Index(docs, FIELDS))
        result = engine.search("x", {"title": 1.0, "body": 1.0}, k=1)
        self.assertEqual(result.meta["mode"], "ta")
        self.assertTrue(result.meta["stopped_early"])
        total_pulls = sum(result.meta["pulls"].values())
        self.assertLess(total_pulls, 2 * 31)
        self.assertEqual(result.items[0].doc_id, "star")


class NormalizationBaselineTest(unittest.TestCase):
    def test_stats_are_explicit_and_full_index_based(self):
        engine = Engine(tiny_index())
        result = engine.search("x", {"title": 1.0, "body": 1.0}, k=1)
        # Stats come from the whole index, declared in the metadata, and do
        # not depend on how many candidates the TA fetched.
        self.assertEqual(result.meta["stats"]["title"], {"min": 0.0, "max": 2.0})
        self.assertEqual(result.meta["stats"]["body"], {"min": 0.0, "max": 2.0})

    def test_index_update_is_an_explicit_new_baseline(self):
        engine = Engine(tiny_index())
        weights = {"title": 1.0, "body": 1.0}
        snap = engine.snapshot("x", weights)
        page_before = engine.page(snap.token, offset=0, limit=2)
        v1_stats = engine.search("x", weights, k=1).meta["stats"]

        bigger = tiny_index()
        docs = [
            Document("d1", {"title": "x x", "body": "y"}),
            Document("d2", {"title": "x", "body": "x x", "notes": "z"}),
            Document("d3", {"title": "y", "body": "x", "notes": "x"}),
            Document("d4", {"title": "x x x x x", "body": "x"}),
        ]
        engine.update_index(Index(docs, FIELDS))

        # Frozen snapshot keeps serving the old baseline: no silent change.
        page_after = engine.page(snap.token, offset=0, limit=2)
        self.assertEqual(page_before.items, page_after.items)
        # New searches explicitly use the new version and its new stats.
        result = engine.search("x", weights, k=1)
        self.assertEqual(result.meta["index_version"], 2)
        self.assertNotEqual(result.meta["stats"], v1_stats)
        self.assertEqual(result.meta["stats"]["title"], {"min": 0.0, "max": 5.0})


class SnapshotPaginationTest(unittest.TestCase):
    def setUp(self):
        rng = random.Random(7)
        self.engine = Engine(random_index(rng, n_docs=25))
        self.weights = {"title": 2.0, "body": 1.0, "notes": 1.5}
        self.snap = self.engine.snapshot("机 machine", self.weights)

    def test_pagination_has_no_duplicates_or_omissions(self):
        collected = []
        offset = 0
        while offset is not None:
            page = self.engine.page(self.snap.token, offset=offset, limit=4)
            collected.extend(page.items)
            offset = page.next_offset
        full = self.engine.exhaustive("机 machine", self.weights, k=None).items
        self.assertEqual(collected, full)
        self.assertEqual(len({sd.doc_id for sd in collected}), len(collected))

    def test_page_beyond_end_is_empty(self):
        page = self.engine.page(self.snap.token, offset=10**6, limit=5)
        self.assertEqual(page.items, [])
        self.assertIsNone(page.next_offset)

    def test_unknown_token_rejected(self):
        with self.assertRaises(SnapshotNotFoundError):
            self.engine.page("v1-s999", offset=0, limit=1)

    def test_invalid_offset_and_limit_rejected(self):
        with self.assertRaises(ValueError):
            self.engine.page(self.snap.token, offset=-1, limit=1)
        with self.assertRaises(ValueError):
            self.engine.page(self.snap.token, offset=0, limit=0)

    def test_snapshot_is_frozen_across_index_updates(self):
        page_before = self.engine.page(self.snap.token, offset=0, limit=3)
        self.engine.update_index(tiny_index())
        page_after = self.engine.page(self.snap.token, offset=0, limit=3)
        self.assertEqual(page_before.items, page_after.items)
        self.assertEqual(page_after.total, page_before.total)


class SubScoreOutputTest(unittest.TestCase):
    def test_items_carry_per_field_sub_scores(self):
        engine = Engine(tiny_index())
        result = engine.search("x", {"title": 1.0, "body": 1.0}, k=3)
        self.assertTrue(result.items)
        for sd in result.items:
            subs = sd.sub_scores()
            self.assertEqual(set(subs), {"body", "title"})
            for fs in sd.field_scores:
                if fs.present:
                    self.assertIsNotNone(fs.raw)
                    self.assertIsNotNone(fs.normalized)
                else:
                    self.assertIsNone(fs.raw)
                    self.assertIsNone(fs.normalized)

    def test_deterministic_repeated_runs(self):
        engine = Engine(tiny_index())
        first = engine.search("x", {"title": 1.0, "body": 1.0}, k=3)
        second = engine.search("x", {"title": 1.0, "body": 1.0}, k=3)
        self.assertEqual(first.items, second.items)


if __name__ == "__main__":
    unittest.main()
