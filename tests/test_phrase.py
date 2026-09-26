import unittest

from invidx import Index, PhraseQuery


def single_doc_index(text, field="f"):
    idx = Index()
    idx.add_document("d", {field: text})
    idx.commit()
    return idx


class TestExactPhrase(unittest.TestCase):
    def test_exact_match(self):
        idx = single_doc_index("the quick brown fox")
        r = idx.search(PhraseQuery.of("quick brown"))
        self.assertEqual(len(r), 1)
        m = r.matches[0]
        self.assertEqual((m.pos_start, m.pos_end), (1, 2))
        self.assertEqual(m.text, "quick brown")

    def test_no_match_when_not_adjacent(self):
        idx = single_doc_index("quick red brown")
        self.assertEqual(len(idx.search(PhraseQuery.of("quick brown"))), 0)

    def test_original_char_range(self):
        text = "  Hello,   wonderful WORLD"
        idx = single_doc_index(text)
        m = idx.search(PhraseQuery.of("wonderful world")).matches[0]
        self.assertEqual(text[m.start_char : m.end_char], "wonderful WORLD")


class TestRepeatedTerms(unittest.TestCase):
    def test_a_a_needs_two_positions(self):
        idx = Index()
        idx.add_document("one", {"f": "a"})
        idx.add_document("two", {"f": "a a"})
        idx.add_document("three", {"f": "a a a"})
        idx.commit()
        got = {}
        for m in idx.search(PhraseQuery.of("a a")):
            got.setdefault(m.docid, []).append((m.pos_start, m.pos_end))
        self.assertNotIn("one", got)
        self.assertEqual(got["two"], [(0, 1)])
        self.assertEqual(sorted(got["three"]), [(0, 1), (1, 2)])

    def test_positions_not_reused_in_evidence(self):
        idx = single_doc_index("x y x")
        for m in idx.search(PhraseQuery.of("x x", slop=2)):
            used = [p for hit in m.evidence for p in hit.positions]
            self.assertEqual(len(used), len(set(used)))


class TestSlop(unittest.TestCase):
    def test_slop_allows_gaps(self):
        idx = single_doc_index("a b c d")
        self.assertEqual(len(idx.search(PhraseQuery.of("a d", slop=1))), 0)
        r = idx.search(PhraseQuery.of("a d", slop=2))
        self.assertEqual(len(r), 1)
        self.assertEqual((r.matches[0].pos_start, r.matches[0].pos_end), (0, 3))

    def test_slop_budget_is_total(self):
        idx = single_doc_index("a x b y c")
        self.assertEqual(len(idx.search(PhraseQuery.of("a b c", slop=1))), 0)
        self.assertEqual(len(idx.search(PhraseQuery.of("a b c", slop=2))), 1)

    def test_minimal_covering_ranges(self):
        idx = single_doc_index("a b b")
        r = idx.search(PhraseQuery.of("a b", slop=1))
        self.assertEqual([(m.pos_start, m.pos_end) for m in r], [(0, 1)])

    def test_all_minimal_ranges_returned(self):
        idx = single_doc_index("a a a")
        r = idx.search(PhraseQuery.of("a a"))
        self.assertEqual(
            sorted((m.pos_start, m.pos_end) for m in r), [(0, 1), (1, 2)]
        )


class TestSynonymGraph(unittest.TestCase):
    def test_single_token_synonyms(self):
        idx = single_doc_index("the quick fox and the fast fox")
        q = PhraseQuery([["quick", "fast"], ["fox"]])
        r = idx.search(q)
        self.assertEqual(len(r), 2)
        chosen = sorted(m.evidence[0].terms[0] for m in r)
        self.assertEqual(chosen, ["fast", "quick"])

    def test_multi_token_synonym_alternative(self):
        idx = single_doc_index("the new york subway and the nyc subway")
        q = PhraseQuery([[("new", "york"), ("nyc",)], ["subway"]])
        r = idx.search(q)
        spans = sorted((m.pos_start, m.pos_end) for m in r)
        self.assertEqual(spans, [(1, 3), (6, 7)])

    def test_evidence_records_positions(self):
        idx = single_doc_index("quick fox")
        m = idx.search(PhraseQuery([["quick", "fast"], ["fox"]])).matches[0]
        self.assertEqual(m.evidence[0].positions, (0,))
        self.assertEqual(m.evidence[1].positions, (1,))
        self.assertEqual(m.evidence[0].slot_index, 0)


if __name__ == "__main__":
    unittest.main()

