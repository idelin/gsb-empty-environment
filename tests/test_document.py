import unittest

from interval_index import parse_document, tokenize

DOC = """id: d1
range: [0,5)
title: 演示

区间索引支持中文、English 与数字 12345。
"""


class TokenizeTest(unittest.TestCase):
    def test_cjk_is_single_character_tokens(self):
        self.assertEqual(tokenize("中文测试"), ["中", "文", "测", "试"])

    def test_ascii_runs_and_numbers(self):
        self.assertEqual(tokenize("hello world_1 42!"),
                         ["hello", "world_1", "42"])

    def test_punctuation_is_separator(self):
        self.assertEqual(tokenize("a，b。c？d-e"), ["a", "b", "c", "d", "e"])

    def test_nfkc_full_width_to_half_width(self):
        self.assertEqual(tokenize("ＡＢＣ１２３"), ["ABC123"])

    def test_preserves_case(self):
        self.assertEqual(tokenize("AbC xYZ"), ["AbC", "xYZ"])


class DocumentParseTest(unittest.TestCase):
    def test_basic_parse(self):
        doc = parse_document(DOC)
        self.assertEqual(doc.doc_id, "d1")
        self.assertEqual((doc.interval.lo, doc.interval.hi), (0, 4))
        self.assertEqual(doc.meta["title"], "演示")
        self.assertIn("中", doc.tokens())
        self.assertIn("文", doc.tokens())

    def test_missing_range(self):
        doc = parse_document("id: d2\n\nbody")
        self.assertTrue(doc.interval.is_missing)

    def test_blank_range_is_missing(self):
        doc = parse_document("id: d3\nrange:   \n\nbody")
        self.assertTrue(doc.interval.is_missing)

    def test_id_fallback(self):
        doc = parse_document("range: [1,2]\n\nx", doc_id="fname")
        self.assertEqual(doc.doc_id, "fname")

    def test_id_required(self):
        with self.assertRaises(ValueError):
            parse_document("range: [1,2]\n\nx")

    def test_bad_header(self):
        with self.assertRaises(ValueError):
            parse_document("no colon here\n\nx")
        with self.assertRaises(ValueError):
            parse_document("bad key: x\n\nx")


if __name__ == "__main__":
    unittest.main()
