import unittest

from fusion.fusion import (
    AssumptionViolatedError,
    exhaustive_topk,
    threshold_topk,
)
from fusion.normalize import FieldStats


class StubView:
    """Hand-controlled per-field view for unit-testing the algorithms."""

    def __init__(self, field, entries, has_field, stats):
        # entries: list of (doc_id, raw) in the order sorted_access yields
        self.field = field
        self._entries = list(entries)
        self._raw = dict(entries)
        self._has = frozenset(has_field)
        self.stats = stats

    @property
    def has_field(self):
        return self._has

    def sorted_access(self):
        yield from self._entries

    def random_access(self, doc_id):
        if doc_id not in self._has:
            return None
        return self._raw.get(doc_id, 0.0)


def make_views():
    # f1: a=10, b=5, c=1 ; f2: a=4, c=8 ; d has f1 only in has_field set
    f1 = StubView(
        "f1",
        [("a", 10.0), ("b", 5.0), ("c", 1.0), ("d", 2.0)],
        has_field={"a", "b", "c", "d"},
        stats=FieldStats(0.0, 10.0),
    )
    # keep sorted order non-increasing: fix d into place
    f1 = StubView(
        "f1",
        [("a", 10.0), ("b", 5.0), ("d", 2.0), ("c", 1.0)],
        has_field={"a", "b", "c", "d"},
        stats=FieldStats(0.0, 10.0),
    )
    f2 = StubView(
        "f2",
        [("c", 8.0), ("a", 4.0), ("b", 1.0)],
        has_field={"a", "b", "c"},  # d lacks f2
        stats=FieldStats(0.0, 8.0),
    )
    return {"f1": f1, "f2": f2}


class ThresholdTopKTest(unittest.TestCase):
    def test_matches_exhaustive_reference(self):
        views = make_views()
        weights = {"f1": 1.0, "f2": 2.0}
        for k in (1, 2, 3, 4, 10):
            ta, _ = threshold_topk(views, weights, k)
            ref, _ = exhaustive_topk(views, weights, k)
            self.assertEqual(
                [(s.doc_id, s.score) for s in ta],
                [(s.doc_id, s.score) for s in ref],
                f"k={k}",
            )

    def test_missing_field_excluded_not_zero_punished(self):
        views = make_views()
        weights = {"f1": 1.0, "f2": 1.0}
        ref, _ = exhaustive_topk(views, weights, None)
        by_id = {sd.doc_id: sd for sd in ref}
        # d has only f1 (raw 2 -> normalized 0.2); missing f2 must be
        # excluded from numerator AND denominator, not counted as 0.
        self.assertAlmostEqual(by_id["d"].score, 0.2)
        self.assertNotAlmostEqual(by_id["d"].score, (0.2 + 0.0) / 2)

    def test_early_stop_pulls_only_what_is_needed(self):
        views = make_views()
        weights = {"f1": 1.0, "f2": 1.0}
        _, meta = threshold_topk(views, weights, 1)
        self.assertTrue(meta["stopped_early"])
        total_entries = 4 + 3
        total_pulls = sum(meta["pulls"].values())
        self.assertLess(total_pulls, total_entries)

    def test_stop_condition_uses_upper_bound(self):
        views = make_views()
        weights = {"f1": 1.0, "f2": 1.0}
        _, meta = threshold_topk(views, weights, 1)
        # Stopping requires kth_score strictly greater than the bound.
        self.assertIsNotNone(meta["bound"])
        self.assertGreater(meta["kth_score"], meta["bound"])

    def test_non_monotone_stream_raises(self):
        bad = StubView(
            "f1",
            [("a", 1.0), ("b", 9.0)],  # increasing: violates the assumption
            has_field={"a", "b"},
            stats=FieldStats(0.0, 9.0),
        )
        ok = StubView(
            "f2", [("a", 1.0)], has_field={"a"}, stats=FieldStats(0.0, 1.0)
        )
        with self.assertRaises(AssumptionViolatedError):
            threshold_topk({"f1": bad, "f2": ok}, {"f1": 1.0, "f2": 1.0}, 2)

    def test_k_zero_returns_empty(self):
        views = make_views()
        items, _ = threshold_topk(views, {"f1": 1.0, "f2": 1.0}, 0)
        self.assertEqual(items, [])

    def test_negative_k_rejected(self):
        views = make_views()
        with self.assertRaises(ValueError):
            threshold_topk(views, {"f1": 1.0}, -1)

    def test_k_larger_than_candidates_returns_all(self):
        views = make_views()
        items, _ = threshold_topk(views, {"f1": 1.0, "f2": 1.0}, 100)
        self.assertEqual(len(items), 4)

    def test_per_field_sub_scores_present(self):
        views = make_views()
        items, _ = threshold_topk(views, {"f1": 1.0, "f2": 1.0}, 1)
        top = items[0]
        self.assertEqual({fs.field for fs in top.field_scores}, {"f1", "f2"})
        for fs in top.field_scores:
            self.assertTrue(fs.present)
            self.assertIsNotNone(fs.normalized)


if __name__ == "__main__":
    unittest.main()
