import unittest

from timemap import Evidence, Interval, MapHistory, PiecewiseMap


def make_history():
    base = PiecewiseMap.build(
        0, 0, [(5, 1), (5, 0), (10, 2), (10, -1)],
        evidence=(Evidence("ingest", "original timeline"),),
    )
    return MapHistory(base)


class TestVersioning(unittest.TestCase):
    def setUp(self):
        self.hist = make_history()
        self.v1 = self.hist.version(1)
        self.v1_answers = {t: self.v1.eval(t) for t in (0, 5, 10, 13, 20, 30)}
        self.v1_pre = str(self.v1.preimage(Interval.point(5)))
        ev = Evidence("editor", "insert ad break pause")
        self.v2_id = self.hist.revise(1, 12, 14, [(2, 0)], reason="ad break", evidence=(ev,))
        self.v2 = self.hist.version(self.v2_id)

    def test_revision_creates_new_version(self):
        self.assertEqual(self.v2_id, 2)
        self.assertEqual(self.hist.latest, 2)

    def test_new_pause_maps_bounds_to_same_point(self):
        self.assertEqual(self.v2.eval(12), self.v2.eval(14))
        self.assertEqual(self.v2.eval(12), 9)

    def test_continuity_after_revision(self):
        self.assertEqual(self.v2.eval(14), 9)
        self.assertEqual(self.v2.eval(20), 21)
        self.assertEqual(self.v2.eval(30), 11)

    def test_old_version_queries_unchanged(self):
        for t, expected in self.v1_answers.items():
            self.assertEqual(self.v1.eval(t), expected)
        self.assertEqual(str(self.v1.preimage(Interval.point(5))), self.v1_pre)
        self.assertEqual(str(self.hist.version(1).image(Interval(0, 30, hi_open=False))),
                         "[0, 25]")

    def test_versions_diverge_only_in_revised_range(self):
        self.assertEqual(self.v1.eval(13), 11)
        self.assertEqual(self.v2.eval(13), 9)
        self.assertEqual(self.v1.eval(0), self.v2.eval(0))

    def test_evidence_attached_and_old_evidence_kept(self):
        sources = [e.source for e in self.hist.evidence_at(self.v2_id, 13)]
        self.assertEqual(sources, ["editor"])
        self.assertEqual([e.source for e in self.hist.evidence_at(self.v2_id, 0)],
                         ["ingest"])
        self.assertEqual([e.source for e in self.hist.evidence_at(1, 13)], ["ingest"])

    def test_revision_log(self):
        (rev,) = self.hist.revisions
        self.assertEqual((rev.version, rev.base_version, rev.start, rev.end),
                         (2, 1, 12, 14))
        self.assertEqual(rev.reason, "ad break")

    def test_stacked_revisions(self):
        v3 = self.hist.revise(self.v2_id, 25, 27, [(2, -1)], reason="rewind")
        m3 = self.hist.version(v3)
        self.assertEqual(m3.eval(26), m3.eval(25) - 1)
        self.assertEqual(self.v2.eval(26), 15)


if __name__ == "__main__":
    unittest.main()
