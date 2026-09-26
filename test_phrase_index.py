"""unittest suite for the positional inverted index (Python 3.11)."""
import unittest

from phraseindex import (
    DecodeCounter,
    Index,
    PhraseQuery,
    tokenize,
    verify_snapshot,
)


def build_index():
    idx = Index()
    idx.add_document(
        "d1",
        {
            "title": "The quick brown fox",
            "body": "The quick brown fox jumps over the lazy dog.",
        },
    )
    idx.add_document("d2", {"body": "a a a"})
    idx.add_document("d3", {"body": "quick red fox and quick blue fox"})
    idx.add_document(
        "d4",
        {
            "title": "hello 世界",
            "body": "Cafe NAIVE 中文分词 quick brown",
        },
    )
    return idx


class TokenizerTest(unittest.TestCase):
    def test_offsets_and_casefold(self):
        text = "The Cafe!"
        toks = tokenize(text)
        self.assertEqual([t.term for t in toks], ["the", "cafe"])
        self.assertEqual(text[toks[0].start : toks[0].end], "The")
        self.assertEqual(text[toks[1].start : toks[1].end], "Cafe")

    def test_utf8_cjk_and_emoji(self):
        text = "你好🙂world 世界"
        toks = tokenize(text)
        # "你好" is a maximal alnum run; the emoji is a separator.
        self.assertEqual([t.term for t in toks], ["你好", "world", "世界"])
        for t in toks:
            self.assertEqual(text[t.start : t.end].casefold(), t.term)


class PhraseMatchTest(unittest.TestCase):
    def setUp(self):
        self.idx = build_index()

    def test_exact_phrase(self):
        ms = self.idx.search(PhraseQuery(["quick", "brown", "fox"]))
        # d1 carries the phrase in both title and body
        self.assertEqual(len(ms), 2)
        self.assertEqual({m.field for m in ms}, {"title", "body"})
        m = next(m for m in ms if m.field == "body")
        self.assertEqual((m.doc_id, m.field), ("d1", "body"))
        self.assertEqual(m.text, "quick brown fox")
        self.assertEqual(m.positions, (1, 2, 3))

    def test_exact_phrase_no_match_when_not_adjacent(self):
        ms = self.idx.search(PhraseQuery(["quick", "fox"]))
        self.assertEqual(ms, [])

    def test_configurable_gap(self):
        q = PhraseQuery(["quick", "fox"], gaps=1)
        ms = self.idx.search(q)
        got = {(m.doc_id, m.positions) for m in ms}
        # gap=1: exactly one token may sit between -> d1 "quick brown fox"
        # and both "quick red/blue fox" in d3
        self.assertEqual(got, {("d1", (1, 3)), ("d3", (0, 2)), ("d3", (4, 6))})
        q2 = PhraseQuery(["quick", "fox"], gaps=2)
        docs = {m.doc_id for m in self.idx.search(q2)}
        self.assertEqual(docs, {"d1", "d3"})

    def test_per_pair_gaps(self):
        idx = Index()
        idx.add_document("x", {"body": "a c b2 b3 d"})
        q = PhraseQuery(["a", "c", "d"], gaps=[0, 2])
        ms = idx.search(q)
        self.assertEqual([m.positions for m in ms], [(0, 1, 4)])

    def test_synonym_position_graph(self):
        q = PhraseQuery([{"quick", "fast"}, "brown"])
        ms = self.idx.search(q)
        self.assertEqual({m.doc_id for m in ms}, {"d1", "d4"})
        ev_terms = {e.term for m in ms for e in m.evidence}
        self.assertEqual(ev_terms, {"quick", "brown"})

    def test_repeated_term_needs_two_positions(self):
        idx = Index()
        idx.add_document("one", {"body": "a"})
        idx.add_document("two", {"body": "a a"})
        ms = idx.search(PhraseQuery(["a", "a"]))
        self.assertEqual(len(ms), 1)
        self.assertEqual(ms[0].doc_id, "two")
        self.assertEqual(ms[0].positions, (0, 1))
        self.assertEqual(len(set(ms[0].positions)), 2)

    def test_repeated_term_all_matches(self):
        ms = self.idx.search(PhraseQuery(["a", "a"]))
        self.assertEqual(
            [m.positions for m in ms if m.doc_id == "d2"], [(0, 1), (1, 2)]
        )

    def test_no_position_reuse_with_synonym_overlap(self):
        idx = Index()
        idx.add_document("s", {"body": "x a"})
        q = PhraseQuery([{"x", "a"}, {"a", "x"}])
        ms = idx.search(q)
        self.assertEqual(len(ms), 1)
        self.assertEqual(ms[0].positions, (0, 1))

    def test_cross_field_cannot_form_phrase(self):
        idx = Index()
        idx.add_document("cf", {"title": "quick", "body": "brown fox"})
        self.assertEqual(idx.search(PhraseQuery(["quick", "brown"])), [])

    def test_minimal_covering_range_and_evidence(self):
        ms = self.idx.search(PhraseQuery(["quick", "fox"], gaps=1))
        m = next(m for m in ms if m.doc_id == "d3" and m.positions == (0, 2))
        self.assertEqual(m.text, "quick red fox")
        self.assertEqual(
            [(e.element, e.term, e.position) for e in m.evidence],
            [(0, "quick", 0), (1, "fox", 2)],
        )
        body = "quick red fox and quick blue fox"
        self.assertEqual(body[m.start : m.end], m.text)

    def test_utf8_offsets(self):
        ms = self.idx.search(PhraseQuery(["中文分词", "quick"], gaps=0))
        self.assertEqual(len(ms), 1)
        body = "Cafe NAIVE 中文分词 quick brown"
        self.assertEqual(body[ms[0].start : ms[0].end], "中文分词 quick")


class IndexLifecycleTest(unittest.TestCase):
    def test_delete_updates_positions_and_df(self):
        idx = Index()
        idx.add_document("p1", {"body": "apple pie"})
        idx.add_document("p2", {"body": "apple tart apple"})
        self.assertEqual(idx.df("apple"), 2)
        idx.delete_document("p2")
        self.assertEqual(idx.df("apple"), 1)
        ms = idx.search(PhraseQuery(["apple"]))
        self.assertEqual([(m.doc_id, m.positions) for m in ms], [("p1", (0,))])

    def test_merge_preserves_results_and_drops_tombstones(self):
        idx = build_index()
        idx.delete_document("d3")
        q = PhraseQuery([{"quick", "fast"}, {"brown", "fox"}], gaps=1)
        before = idx.search(q)
        self.assertGreaterEqual(idx.num_segments, 2)
        idx.merge()
        self.assertEqual(idx.num_segments, 1)
        after = idx.search(q)
        self.assertEqual(before, after)
        seg = idx._segments[0]
        self.assertNotIn("d3", seg.postings.get("quick", {}))
        self.assertNotIn("d3", seg.docs)
        self.assertEqual(idx.df("quick"), 2)

    def test_snapshot_is_stable_and_cursor_resumes(self):
        idx = build_index()
        snap = idx.snapshot()
        q = PhraseQuery(["quick", "fox"], gaps=2)
        snap_before = snap.search(q)
        idx.add_document("d5", {"body": "quick gray fox"})
        idx.delete_document("d1")
        self.assertEqual(snap.search(q), snap_before)
        live = idx.search(q)
        self.assertEqual({m.doc_id for m in live}, {"d3", "d5"})
        self.assertEqual({m.doc_id for m in snap_before}, {"d1", "d3"})
        page1 = snap.search(PhraseQuery(["quick"]), after_doc=None)
        first_doc = page1[0].doc_id
        rest = snap.search(PhraseQuery(["quick"]), after_doc=first_doc)
        # resume token = last seen doc_id; the resumed page contains only
        # strictly later docs, and both pages partition the full result
        self.assertTrue(all(m.doc_id > first_doc for m in rest))
        self.assertEqual(
            sorted(
                rest + [m for m in page1 if m.doc_id <= first_doc],
                key=lambda m: (m.doc_id, m.field, m.positions),
            ),
            page1,
        )


class VerificationTest(unittest.TestCase):
    def test_bruteforce_crosscheck_mixed_corpus(self):
        idx = build_index()
        idx.add_document("d5", {"body": "a a quick a a brown a"})
        idx.delete_document("d4")
        for q in (
            PhraseQuery(["quick", "brown"]),
            PhraseQuery(["quick", "fox"], gaps=2),
            PhraseQuery([{"quick", "a"}, {"brown", "a"}], gaps=1),
            PhraseQuery(["a", "a"]),
            PhraseQuery(["a", "a", "a"], gaps=[0, 1]),
        ):
            report = verify_snapshot(idx.snapshot(), q)
            self.assertTrue(report.ok, msg=f"{q}: {report.mismatches}")
            self.assertGreater(report.docs_checked, 0)

    def test_high_frequency_repeated_term_decode_blocks(self):
        n = 500
        idx = Index()
        idx.add_document("hot", {"body": " ".join(["a"] * n)})
        idx.add_document("cold", {"body": "nothing here"})
        report = verify_snapshot(idx.snapshot(), PhraseQuery(["a", "a"]))
        self.assertTrue(report.ok, msg=str(report.mismatches))
        self.assertEqual(report.matches_expected, n - 1)
        self.assertEqual(report.matches_actual, n - 1)
        self.assertGreater(report.blocks_decoded, 0)
        print(
            f"\n[high-freq 'a a' x{n}] matches={report.matches_actual} "
            f"blocks_decoded={report.blocks_decoded} "
            f"positions_scanned={report.positions_scanned}"
        )

    def test_skipping_avoids_cartesian_blowup(self):
        idx = Index()
        idx.add_document(
            "big", {"body": " ".join(["a"] * 500 + ["b"] * 500)}
        )
        counter = DecodeCounter()
        ms = idx.search(PhraseQuery(["a", "b"]), counter=counter)
        self.assertEqual([m.positions for m in ms], [(499, 500)])
        self.assertLess(counter.positions_scanned, 500 + 500 + 16)
        print(
            f"\n[skip check 'a b'] positions_scanned="
            f"{counter.positions_scanned} blocks_decoded={counter.blocks_decoded}"
        )


if __name__ == "__main__":
    unittest.main(verbosity=2)
