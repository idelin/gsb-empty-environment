import tempfile
import unittest
from pathlib import Path

from simcluster import (
    Document,
    build_report,
    cluster,
    find_candidates,
    jaccard,
    load_documents,
    run,
    shingle_set,
    tokenize,
    verify_candidates,
)


def make_doc(doc_id, text, k=3):
    tokens = tokenize(text)
    return Document(doc_id=doc_id, text=text, tokens=tokens, shingles=shingle_set(tokens, k))


class TokenizeTest(unittest.TestCase):
    def test_casefold_and_punctuation(self):
        self.assertEqual(tokenize("Hello, WORLD! foo_bar 42."), ["hello", "world", "foo_bar", "42"])

    def test_unicode(self):
        self.assertEqual(tokenize("量子 计算 Café"), ["量子", "计算", "café"])

    def test_empty(self):
        self.assertEqual(tokenize(""), [])
        self.assertEqual(tokenize("! ? …"), [])


class ShingleTest(unittest.TestCase):
    def test_normal_width(self):
        tokens = ["a", "b", "c", "d"]
        self.assertEqual(
            shingle_set(tokens, 3),
            {("a", "b", "c"), ("b", "c", "d")},
        )

    def test_width_one(self):
        self.assertEqual(shingle_set(["a", "b", "a"], 1), {("a",), ("b",)})

    def test_shorter_than_width_defined(self):
        # Defined fallback: the whole token sequence is the single shingle.
        self.assertEqual(shingle_set(["only", "two"], 3), {("only", "two")})
        self.assertEqual(shingle_set(["one"], 5), {("one",)})

    def test_empty_tokens(self):
        self.assertEqual(shingle_set([], 3), set())

    def test_invalid_width(self):
        with self.assertRaises(ValueError):
            shingle_set(["a"], 0)


class JaccardTest(unittest.TestCase):
    def test_known_value(self):
        self.assertAlmostEqual(jaccard({1, 2, 3}, {2, 3, 4}), 2 / 4)

    def test_identical(self):
        self.assertEqual(jaccard({1, 2}, {1, 2}), 1.0)

    def test_disjoint(self):
        self.assertEqual(jaccard({1}, {2}), 0.0)

    def test_both_empty_defined(self):
        self.assertEqual(jaccard(set(), set()), 1.0)

    def test_one_empty(self):
        self.assertEqual(jaccard(set(), {1}), 0.0)


class HashCollisionTest(unittest.TestCase):
    def test_collision_only_adds_candidates_not_edges(self):
        # A constant hash forces EVERY pair to be a candidate (maximal
        # collision).  Exact verification must still reject dissimilar pairs.
        a = make_doc("a.txt", "red green blue yellow black white purple")
        b = make_doc("b.txt", "red green blue yellow black white purple")
        c = make_doc("c.txt", "apple banana cherry grape melon lemon mango")
        docs = [a, b, c]

        candidates = find_candidates(docs, hash_fn=lambda shingle: 0)
        self.assertEqual(candidates, {(0, 1), (0, 2), (1, 2)})

        edges = verify_candidates(docs, candidates, threshold=0.5)
        pairs = {tuple(sorted((e.doc_a, e.doc_b))) for e in edges}
        self.assertEqual(pairs, {("a.txt", "b.txt")})


class TransitivityTest(unittest.TestCase):
    def test_chain_cluster_without_phantom_edge(self):
        # A~B and B~C but NOT A~C: one cluster, exactly two edges.
        a = make_doc("a.txt", "w1 w2 w3 w4 w5 w6")
        b = make_doc("b.txt", "w1 w2 w3 w4 x5 x6")
        c = make_doc("c.txt", "y1 y2 w3 w4 x5 x6")
        docs = [a, b, c]

        edges = verify_candidates(docs, find_candidates(docs), threshold=0.3)
        pairs = {tuple(sorted((e.doc_a, e.doc_b))) for e in edges}
        self.assertEqual(pairs, {("a.txt", "b.txt"), ("b.txt", "c.txt")})
        self.assertNotIn(("a.txt", "c.txt"), pairs)

        clusters = cluster(docs, edges)
        self.assertEqual(clusters, [["a.txt", "b.txt", "c.txt"]])

        report = build_report(docs, edges, k=3, threshold=0.3)
        evidence_pairs = {tuple(sorted(e["pair"])) for e in report["evidence"]}
        self.assertNotIn(("a.txt", "c.txt"), evidence_pairs)
        self.assertIn("does NOT imply", report["rule"])


class EndToEndTest(unittest.TestCase):
    def test_run_on_synthetic_utf8_docs(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "x.txt").write_text("one two three four five six", encoding="utf-8")
            (root / "y.txt").write_text("one two three four five six", encoding="utf-8")
            (root / "z.txt").write_text("完全 不同 的 内容 没有 重叠 部分", encoding="utf-8")
            (root / "short.txt").write_text("tiny doc", encoding="utf-8")
            (root / "empty.txt").write_text("", encoding="utf-8")

            report = run(sorted(root.glob("*.txt")), k=3, threshold=0.5)
            pairs = {tuple(sorted(e["pair"])) for e in report["evidence"]}
            self.assertEqual(pairs, {("x.txt", "y.txt")})
            self.assertEqual(report["evidence"][0]["jaccard"], 1.0)

    def test_short_docs_compare_by_fallback_shingle(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "s1.txt").write_text("tiny doc", encoding="utf-8")
            (root / "s2.txt").write_text("tiny doc", encoding="utf-8")
            (root / "s3.txt").write_text("other words", encoding="utf-8")
            report = run(sorted(root.glob("*.txt")), k=3, threshold=0.5)
            pairs = {tuple(sorted(e["pair"])) for e in report["evidence"]}
            self.assertEqual(pairs, {("s1.txt", "s2.txt")})

    def test_load_documents_reads_utf8(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "u.txt"
            path.write_text("naïve café 量子", encoding="utf-8")
            docs = load_documents([path], k=2)
            self.assertEqual(docs[0].tokens, ["naïve", "café", "量子"])


if __name__ == "__main__":
    unittest.main()
