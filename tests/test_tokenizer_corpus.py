import unittest

from fusion import load_corpus, tokenize


class TestTokenizer(unittest.TestCase):
    def test_latin_word_runs_and_casefold(self):
        self.assertEqual(tokenize("Full-Text2"), ["full", "text2"])

    def test_cjk_single_chars(self):
        self.assertEqual(tokenize("全文检索"), ["全", "文", "检", "索"])

    def test_nfkc_fullwidth(self):
        self.assertEqual(tokenize("ＡＢＣ"), ["abc"])

    def test_underscore_and_punct_are_separators(self):
        self.assertEqual(tokenize("a_b.c"), ["a", "b", "c"])

    def test_empty(self):
        self.assertEqual(tokenize(""), [])
        self.assertEqual(tokenize("   ..."), [])

    def test_mixed(self):
        self.assertEqual(
            tokenize("CAFÉ 全文 abc"),
            ["café", "全", "文", "abc"],
        )

    def test_deterministic(self):
        t = tokenize("全文检索 Search 检索")
        self.assertEqual(t, tokenize("全文检索 Search 检索"))


class TestCorpus(unittest.TestCase):
    def test_load(self):
        docs = load_corpus("data/corpus.tsv")
        ids = [d.doc_id for d in docs]
        self.assertEqual(ids[0], "d01")
        self.assertIn("title", docs[0].fields)
        self.assertIn("body", docs[0].fields)

    def test_missing_field_is_absent(self):
        docs = {d.doc_id: d for d in load_corpus("data/corpus.tsv")}
        self.assertNotIn("nonexistent", docs["d01"].fields)

    def test_bad_line(self, ):
        import os
        import tempfile
        with tempfile.NamedTemporaryFile("w", suffix=".tsv",
                                         encoding="utf-8", delete=False) as fh:
            fh.write("only_one_column\n")
            path = fh.name
        try:
            with self.assertRaises(ValueError):
                load_corpus(path)
        finally:
            os.unlink(path)


if __name__ == "__main__":
    unittest.main()
