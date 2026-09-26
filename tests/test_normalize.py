import unittest

from normsearch import normalize_with_map


class NormalizeTests(unittest.TestCase):
    def test_casefold_and_decomposition(self):
        view = normalize_with_map("CAFÉ")
        self.assertEqual(view.normalized, "cafe")
        # The final 'e' comes from the single precomposed char U+00C9.
        self.assertEqual(view.map_range(3, 4), (3, 4))

    def test_decomposed_input_with_combining_mark(self):
        view = normalize_with_map("cafe\u0301")  # e + combining acute
        self.assertEqual(view.normalized, "cafe")
        self.assertEqual(view.source_spans[3], (3, 4))

    def test_casefold_expansion_many_to_one(self):
        view = normalize_with_map("STRAßE")
        self.assertEqual(view.normalized, "strasse")
        # Both output 's' chars originate from U+00DF at UTF-16 unit 4.
        self.assertEqual(view.source_spans[4], (4, 5))
        self.assertEqual(view.source_spans[5], (4, 5))
        self.assertEqual(view.map_range(4, 6), (4, 5))

    def test_ligature_decomposition(self):
        view = normalize_with_map("ﬁle")
        self.assertEqual(view.normalized, "file")
        self.assertEqual(view.source_spans[0], (0, 1))
        self.assertEqual(view.source_spans[1], (0, 1))

    def test_deleted_control_and_format_chars_do_not_shift(self):
        view = normalize_with_map("Stra\u00adße")  # soft hyphen inserted
        self.assertEqual(view.normalized, "strasse")
        # U+00AD at UTF-16 unit 4 is deleted; ß starts at unit 5.
        self.assertEqual(view.source_spans[4], (5, 6))
        self.assertEqual(view.map_range(0, 6), (0, 6))

    def test_astral_char_counts_two_utf16_units(self):
        view = normalize_with_map("😀caf")
        self.assertEqual(view.normalized, "😀caf")
        # 'c' starts after the 2 UTF-16 units of the surrogate pair.
        self.assertEqual(view.source_spans[1], (2, 3))

    def test_dotted_capital_i(self):
        self.assertEqual(normalize_with_map("İST").normalized, "ist")

    def test_empty(self):
        view = normalize_with_map("")
        self.assertEqual(view.normalized, "")
        with self.assertRaises(IndexError):
            view.map_range(0, 0)


if __name__ == "__main__":
    unittest.main()
