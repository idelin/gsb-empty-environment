import unittest

from syno.tokenizer import tokenize


class TokenizerTest(unittest.TestCase):
    def test_ascii_words_and_numbers(self):
        toks = tokenize("Fast-Car 123!")
        self.assertEqual([t.text for t in toks], ["fast", "car", "123"])
        self.assertEqual([t.kind for t in toks], ["word", "word", "word"])
        self.assertEqual([t.pos for t in toks], [0, 1, 2])

    def test_cjk_split_per_character(self):
        toks = tokenize("机器学习。")
        self.assertEqual([t.text for t in toks], ["机", "器", "学", "习"])
        self.assertTrue(all(t.kind == "cjk" for t in toks))

    def test_offsets_roundtrip(self):
        text = "New York, NYC!"
        toks = tokenize(text)
        # fold 后 new 仍在偏移 0..3
        self.assertEqual(text[0:3].casefold(), toks[0].text)
        nyc = [t for t in toks if t.text == "nyc"][0]
        self.assertEqual(text[nyc.start:nyc.end].casefold(), "nyc")

    def test_casefold(self):
        self.assertEqual([t.text for t in tokenize("ML ml Ml")], ["ml", "ml", "ml"])

    def test_punctuation_and_whitespace_dropped(self):
        self.assertEqual([t.text for t in tokenize("a , b\n c")], ["a", "b", "c"])


if __name__ == "__main__":
    unittest.main()
