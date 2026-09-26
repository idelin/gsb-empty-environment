import unicodedata
import unittest

from minisearch.normalize import (
    Alignment,
    Link,
    normalize_document,
    token_spans,
    utf16_prefix,
)


def round_trip_utf8(text: str) -> str:
    """Documents are synthesized locally and carried as UTF-8 bytes."""
    return text.encode("utf-8").decode("utf-8")


class NormalizeBasicTest(unittest.TestCase):
    def test_precomposed_and_decomposed_equal(self):
        pre, align_pre = normalize_document(round_trip_utf8("Caf\u00e9"))
        dec, align_dec = normalize_document(round_trip_utf8("Cafe\u0301"))
        self.assertEqual(pre, "cafe\u0301")
        self.assertEqual(pre, dec)
        # Precomposed: 4 original cp -> 5 normalized cp.
        self.assertEqual(align_pre.map_span(0, 5), (0, 4))
        # Decomposed: 5 original cp -> 5 normalized cp.
        self.assertEqual(align_dec.map_span(0, 5), (0, 5))

    def test_compatibility_decomposition_ligature(self):
        normalized, alignment = normalize_document("\ufb03le")  # ffi + le
        self.assertEqual(normalized, "ffile")
        self.assertEqual(alignment.map_span(0, 3), (0, 1))  # ﬁ -> ffi
        self.assertEqual(alignment.map_span(0, 5), (0, 3))

    def test_casefold_expansion_sharp_s(self):
        normalized, alignment = normalize_document("Stra\u00dfe")
        self.assertEqual(normalized, "strasse")
        # The single ß fans out to two normalized code points.
        self.assertEqual(alignment.link(4), Link(4, 5))
        self.assertEqual(alignment.link(5), Link(4, 5))
        self.assertEqual(alignment.map_span(4, 6), (4, 5))

    def test_casefold_expansion_dotted_capital_i(self):
        normalized, alignment = normalize_document("\u0130")
        self.assertEqual(normalized, "i\u0307")
        self.assertEqual(alignment.map_span(0, 2), (0, 1))

    def test_fullwidth_compatibility(self):
        normalized, _ = normalize_document("\uff21\uff22\uff23")  # ＡＢＣ
        self.assertEqual(normalized, "abc")

    def test_soft_hyphen_and_zero_width_are_deleted(self):
        normalized, alignment = normalize_document("na\u00adive\u200bx")
        self.assertEqual(normalized, "naivex")
        # Full mapped range still covers every deleted code point.
        self.assertEqual(alignment.map_span(0, 6), (0, 7))

    def test_inline_control_kept_inside_range(self):
        # NUL is Cc (non-whitespace): deleted, same fragment.
        normalized, alignment = normalize_document("a\x00b")
        self.assertEqual(normalized, "ab")
        self.assertEqual(alignment.map_span(0, 2), (0, 3))

    def test_whitespace_control_splits_fragments(self):
        # \n is whitespace: two fragments joined by a synthetic space.
        normalized, alignment = normalize_document("a\nb")
        self.assertEqual(normalized, "a b")
        self.assertTrue(alignment.link(1).inserted)
        span = alignment.map_span(0, 3)
        self.assertEqual(span, (0, 3))  # cross-fragment range keeps the gap

    def test_inserted_only_span_is_unmappable(self):
        _, alignment = normalize_document("a\nb")
        with self.assertRaises(ValueError):
            alignment.map_span(1, 2)

    def test_synthetic_space_bounds_are_filtered(self):
        # Ending on the inserted space gives the same endpoint as without it.
        _, alignment = normalize_document("a\tb")
        self.assertEqual(alignment.map_span(0, 2), (0, 1))
        self.assertEqual(alignment.map_span(2, 3), (2, 3))

    def test_canonical_ordering_matches_unicodedata(self):
        # Acute (ccc 230) before dot below (ccc 220) must be reordered.
        text = "a\u0301\u0323z"
        normalized, _ = normalize_document(text)
        self.assertEqual(normalized, unicodedata.normalize("NFKD", text) + "z")

    def test_nonbreaking_space_decomposes_to_space(self):
        normalized, alignment = normalize_document("a\u00a0b")
        self.assertEqual(normalized, "a b")
        # Not an inserted char: U+00A0 is a real original code point.
        self.assertFalse(alignment.link(1).inserted)
        self.assertEqual(alignment.map_span(0, 3), (0, 3))

    def test_empty_input(self):
        normalized, alignment = normalize_document("")
        self.assertEqual(normalized, "")
        self.assertEqual(len(alignment), 0)

    def test_tokens(self):
        normalized, _ = normalize_document("Caf\u00e9, bar123!")
        self.assertEqual(
            [normalized[a:b] for a, b in token_spans(normalized)],
            ["cafe\u0301", "bar123"],
        )

    def test_utf16_prefix_astral(self):
        text = "\U0001f600 Caf\u00e9"
        prefix = utf16_prefix(text)
        self.assertEqual(prefix[0], 0)
        self.assertEqual(prefix[1], 2)   # after emoji
        self.assertEqual(prefix[2], 3)   # after space
        self.assertEqual(prefix[len(text)], 9)


if __name__ == "__main__":
    unittest.main()
