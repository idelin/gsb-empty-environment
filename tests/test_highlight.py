import html
import re
import unittest

from normsearch import Index, merge_ranges, render_html, utf16_slice


class HighlightTests(unittest.TestCase):
    def test_script_tag_is_escaped_not_executed(self):
        out = render_html("safe <script>alert(1)</script>", [(5, 30)])
        self.assertNotIn("<script>", out)
        self.assertNotIn("</script>", out)
        self.assertIn("&lt;script&gt;", out)
        self.assertIn("&lt;/script&gt;", out)

    def test_match_wrapped_in_mark(self):
        index = Index()
        index.add_segment("v1", "die straße")
        out = render_html(index.build().original, index.search("strasse"))
        self.assertEqual(out, "die <mark>straße</mark>")

    def test_overlapping_and_adjacent_ranges_merge(self):
        self.assertEqual(merge_ranges([(0, 2), (1, 3), (5, 6), (6, 7)]),
                         [(0, 3), (5, 7)])

    def test_astral_offsets_never_split_surrogate_pair(self):
        index = Index()
        index.add_segment("v1", "😀 STRASSE")
        ranges = index.search("strasse")
        self.assertEqual(ranges, [(3, 10)])
        out = render_html(index.build().original, ranges)
        self.assertEqual(out, "😀 <mark>STRASSE</mark>")
        self.assertEqual(utf16_slice(index.build().original, 0, 2), "😀")

    def test_escaped_output_remains_traceable_to_utf16(self):
        index = Index()
        index.add_segment("v1", "<b>café</b> & straße")
        ranges = index.search("cafe") + index.search("strasse")
        out = render_html(index.build().original, ranges)
        # Strip the only tags we inserted, unescape: original text recovered.
        recovered = html.unescape(re.sub(r"</?mark>", "", out))
        self.assertEqual(recovered, index.build().original)
        # Every marked slice corresponds to the documented UTF-16 ranges.
        marked = [html.unescape(m) for m in re.findall(r"<mark>(.*?)</mark>",
                                                       out, re.S)]
        text = index.build().original
        self.assertEqual(
            marked,
            [utf16_slice(text, s, e) for s, e in merge_ranges(ranges)],
        )


if __name__ == "__main__":
    unittest.main()
