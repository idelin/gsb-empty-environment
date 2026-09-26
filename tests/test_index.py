import unittest

from syno import PositionalIndex


class IndexTest(unittest.TestCase):
    def setUp(self):
        self.idx = PositionalIndex()
        self.idx.add_document("d1", "fast car fast")
        self.idx.add_document("d2", "a fast car and another car")

    def test_postings_positions(self):
        self.assertEqual(self.idx.term_positions("d1", "fast"), [0, 2])
        self.assertEqual(self.idx.term_positions("d2", "car"), [2, 5])
        self.assertEqual(self.idx.term_positions("d1", "missing"), [])

    def test_phrase_positions_contiguous_only(self):
        self.assertEqual(self.idx.phrase_positions("d2", ("fast", "car")), [1])
        self.assertEqual(self.idx.phrase_positions("d1", ("fast", "car")), [0])

    def test_phrase_rejects_noncontiguous(self):
        idx = PositionalIndex()
        idx.add_document("d", "car fast car")
        # "fast car" 连续存在；"car car" 不连续，不得匹配
        self.assertEqual(idx.phrase_positions("d", ("fast", "car")), [1])
        self.assertEqual(idx.phrase_positions("d", ("car", "car")), [])

    def test_cjk_phrase_positions(self):
        idx = PositionalIndex()
        idx.add_document("c", "我学机器学习")
        self.assertEqual(idx.phrase_positions("c", ("机", "器", "学", "习")), [2])

    def test_offsets_and_text_at(self):
        idx = PositionalIndex()
        idx.add_document("d", "New York!")
        text, st, en = idx.text_at("d", 0, 2)
        self.assertEqual(text, "New York")
        self.assertEqual((st, en), (0, 8))

    def test_duplicate_doc_id_rejected(self):
        with self.assertRaises(ValueError):
            self.idx.add_document("d1", "again")


if __name__ == "__main__":
    unittest.main()
