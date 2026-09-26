import unittest

from normsearch import Index, VersionMismatchError


class IndexTests(unittest.TestCase):
    def test_version_mismatch_rejected(self):
        index = Index()
        index.add_segment("v1", "Straße")
        with self.assertRaises(VersionMismatchError):
            index.add_segment("v2", " cafe")

    def test_old_and_new_indexes_stay_separate(self):
        old = Index()
        old.add_segment("v1", "CAFE")
        new = Index()
        new.add_segment("v2", "café")
        self.assertEqual(old.doc_version, "v1")
        self.assertEqual(new.search("cafe"), [(0, 4)])
        self.assertEqual(old.search("cafe"), [(0, 4)])

    def test_cross_segment_match_keeps_full_range(self):
        index = Index()
        index.add_segment("v1", "Stra")
        index.add_segment("v1", "ße und CAFÉ")
        self.assertEqual(index.search("strasse"), [(0, 6)])
        self.assertEqual(index.search("cafe"), [(11, 15)])

    def test_overlapping_matches_are_separate_until_merge(self):
        index = Index()
        index.add_segment("v1", "aaaa")
        self.assertEqual(index.search("aa"), [(0, 2), (1, 3), (2, 4)])

    def test_tokens_maximal_alnum_runs(self):
        index = Index()
        index.add_segment("v1", "café 99 ﬁ.x")
        tokens = index.iter_tokens()
        self.assertEqual([t[0] for t in tokens], ["cafe", "99", "fi", "x"])
        # 'fi' comes from the single ligature U+FB01.
        fi = [t for t in tokens if t[0] == "fi"][0]
        self.assertEqual((fi[1], fi[2]), (8, 9))

    def test_empty_query(self):
        index = Index()
        index.add_segment("v1", "cafe")
        self.assertEqual(index.search(""), [])
        self.assertEqual(index.search("\u00ad\u0301"), [])


if __name__ == "__main__":
    unittest.main()
