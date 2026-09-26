"""Randomized cross-checks against an independent naive implementation.

The naive reference derives UTF-16 widths from UTF-16-LE byte lengths
(instead of comparing code points) and builds the source-span table with its
own loop. Disagreement means the production mapper is wrong.
"""

import html
import random
import re
import unittest
import unicodedata

from normsearch import (
    Index,
    merge_ranges,
    normalize_with_map,
    render_html,
    utf16_slice,
)

ALPHABET = [
    "a", "z", "A", "Z", "ß", "é", "É", "É", "ﬁ", "İ", "i",
    "\u0301", "\u00ad", "\u200b", "\t", "\n", "\x00", " ",
    "9", ".", "<", ">", "&", '"',
    "😀", "𠀀", "é",
]


def naive_normalize_piece(ch):
    folded = ch.casefold()
    decomposed = unicodedata.normalize("NFKD", folded)
    out = []
    for c in decomposed:
        cat = unicodedata.category(c)
        if cat in ("Cc", "Cf"):
            continue
        if unicodedata.combining(c):
            continue
        out.append(c)
    return "".join(out)


def naive_map(text):
    """Independent reference: widths via UTF-16-LE bytes, own span loop."""
    normalized, spans = "", []
    cursor = 0
    for ch in text:
        width = len(ch.encode("utf-16-le")) // 2
        lo, hi = cursor, cursor + width
        cursor = hi
        if unicodedata.category(ch) in ("Cc", "Cf"):
            continue
        for out_ch in naive_normalize_piece(ch):
            normalized += out_ch
            spans.append((lo, hi))
    return normalized, tuple(spans)


class PropertyTests(unittest.TestCase):
    def test_random_strings_match_naive_mapper(self):
        rng = random.Random(20260926)
        for _ in range(300):
            text = "".join(rng.choice(ALPHABET) for _ in range(rng.randrange(32)))
            view = normalize_with_map(text)
            naive_text, naive_spans = naive_map(text)
            self.assertEqual(view.normalized, naive_text)
            self.assertEqual(view.source_spans, naive_spans)
            self._check_utf16_boundaries(text, view)
            self._check_random_queries(rng, text, view)

    def _check_utf16_boundaries(self, text, view):
        encoded = text.encode("utf-16-le")
        for lo, hi in view.source_spans:
            decoded = encoded[lo * 2:hi * 2].decode("utf-16-le")
            self.assertEqual(len(decoded), 1)  # span covers whole chars only

    def _check_random_queries(self, rng, text, view):
        for _ in range(4):
            if not view.normalized:
                return
            if rng.random() < 0.6:
                a = rng.randrange(len(view.normalized))
                b = rng.randrange(a + 1, len(view.normalized) + 1)
                needle = view.normalized[a:b]
            else:
                needle = normalize_with_map(
                    "".join(rng.choice(ALPHABET)
                            for _ in range(rng.randrange(1, 5)))
                ).normalized
            if not needle:
                continue
            pos = 0
            while True:
                hit = view.normalized.find(needle, pos)
                if hit == -1:
                    break
                lo, hi = view.map_range(hit, hit + len(needle))
                spans = view.source_spans[hit:hit + len(needle)]
                self.assertEqual((lo, hi), (spans[0][0], spans[-1][1]))
                source = utf16_slice(text, lo, hi)
                self.assertIn(needle, normalize_with_map(source).normalized)
                pos = hit + 1

    def test_random_html_is_escaped_and_roundtrips(self):
        rng = random.Random(77)
        for _ in range(100):
            text = "".join(rng.choice(ALPHABET)
                           for _ in range(rng.randrange(1, 24)))
            index = Index()
            index.add_segment("v", text)
            a = rng.randrange(len(text) + 1)
            b = rng.randrange(len(text) + 1)
            query = text[min(a, b):max(a, b)]
            ranges = index.search(query)
            if not ranges:
                continue
            out = render_html(text, ranges)
            # No executable markup from the source may survive escaping.
            self.assertNotIn("<script", out.lower())
            self.assertNotIn("onerror", out)
            # Removing our tags and unescaping recovers the exact original.
            recovered = html.unescape(re.sub(r"</?mark>", "", out))
            self.assertEqual(recovered, text)
            encoded = text.encode("utf-16-le")
            for lo, hi in merge_ranges(ranges):
                encoded[lo * 2:hi * 2].decode("utf-16-le")  # valid char range

    def test_cross_segment_random_split(self):
        rng = random.Random(9001)
        for _ in range(100):
            text = "".join(rng.choice(ALPHABET)
                           for _ in range(rng.randrange(2, 30)))
            cut = rng.randrange(len(text) + 1)
            whole = Index()
            whole.add_segment("v", text)
            split = Index()
            split.add_segment("v", text[:cut])
            split.add_segment("v", text[cut:])
            query = "".join(rng.choice(ALPHABET)
                            for _ in range(rng.randrange(1, 4)))
            self.assertEqual(whole.search(query), split.search(query))


if __name__ == "__main__":
    unittest.main()
