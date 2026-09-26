import random
import unittest

from invidx import BLOCK_SIZE, Index, PhraseQuery
from invidx.verify import compare_document


def random_query(rng, vocab):
    n_slots = rng.randint(1, 3)
    slots = []
    for _ in range(n_slots):
        n_alts = rng.randint(1, 2)
        alts = []
        for _ in range(n_alts):
            if rng.random() < 0.2:
                alts.append((rng.choice(vocab), rng.choice(vocab)))
            else:
                alts.append((rng.choice(vocab),))
        slots.append(alts)
    return PhraseQuery(slots, slop=rng.randint(0, 3))


class TestRandomizedCrossCheck(unittest.TestCase):
    def test_index_matches_brute_force(self):
        rng = random.Random(20260926)
        vocab = ["a", "b", "c"]
        for trial in range(300):
            n_tokens = rng.randint(0, 14)
            text1 = " ".join(rng.choice(vocab) for _ in range(n_tokens))
            text2 = " ".join(rng.choice(vocab) for _ in range(rng.randint(0, 6)))
            fields = {"f1": text1, "f2": text2}
            idx = Index()
            idx.add_document("doc", fields)
            idx.commit()
            query = random_query(rng, vocab)
            matches = idx.search(query).matches
            ok, actual, expected = compare_document(fields, matches, query)
            self.assertTrue(
                ok,
                f"trial={trial} query={query.slots} slop={query.slop}\n"
                f"fields={fields!r}\nactual={actual}\nexpected={expected}",
            )

    def test_multi_doc_multi_segment_cross_check(self):
        rng = random.Random(7)
        vocab = ["x", "y", "z"]
        idx = Index()
        docs = {}
        for i in range(12):
            text = " ".join(rng.choice(vocab) for _ in range(rng.randint(1, 12)))
            docs[f"doc{i}"] = {"body": text}
            idx.add_document(f"doc{i}", {"body": text})
            if i % 3 == 2:
                idx.commit()
        idx.commit()
        idx.delete("doc3")
        idx.delete("doc7")
        del docs["doc3"]
        del docs["doc7"]
        for _ in range(60):
            query = random_query(rng, vocab)
            matches = idx.search(query).matches
            by_doc = {}
            for m in matches:
                by_doc.setdefault(m.docid, []).append(m)
            self.assertEqual(set(by_doc) - set(docs), set())
            for docid, fields in docs.items():
                ok, actual, expected = compare_document(
                    fields, by_doc.get(docid, []), query
                )
                self.assertTrue(
                    ok,
                    f"doc={docid} query={query.slots} slop={query.slop}\n"
                    f"actual={actual}\nexpected={expected}",
                )


class TestHighFrequencyRepeatedTerm(unittest.TestCase):
    """极高频重复词：按位置跳跃应只解码极少数块，并记录解码块数。"""

    REPEAT = 20000

    def build_index(self):
        text = "x " * self.REPEAT + "y " + "x " * self.REPEAT
        idx = Index()
        idx.add_document("hot", {"f": text})
        idx.commit()
        return idx

    def test_skip_avoids_full_scan(self):
        idx = self.build_index()
        total_blocks = idx._segments[0].terms["x"]["hot"]["f"].total_blocks()
        self.assertEqual(
            total_blocks, (2 * self.REPEAT + BLOCK_SIZE - 1) // BLOCK_SIZE
        )

        result = idx.search(PhraseQuery.of("x y x"))
        spans = [(m.pos_start, m.pos_end) for m in result]
        self.assertEqual(spans, [(self.REPEAT - 1, self.REPEAT + 1)])

        stats = result.stats
        print(
            "\n[high-freq] total x-blocks=%d, blocks_decoded=%d, "
            "positions_scanned=%d, per_term=%s"
            % (
                total_blocks,
                stats.blocks_decoded,
                stats.positions_scanned,
                stats.per_term_blocks,
            )
        )
        self.assertLess(stats.blocks_decoded, 20)
        self.assertLess(stats.positions_scanned, 100)

    def test_high_freq_matches_brute_force(self):
        idx = self.build_index()
        query = PhraseQuery.of("x y x")
        matches = idx.search(query).matches
        text = "x " * self.REPEAT + "y " + "x " * self.REPEAT
        ok, actual, expected = compare_document({"f": text}, matches, query)
        self.assertTrue(ok, f"actual={actual} expected={expected}")


if __name__ == "__main__":
    unittest.main()

