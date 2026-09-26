import unittest

from minisearch import And, Engine, Fuzzy, Index, Not, Or, Phrase, Term


def build_index() -> Index:
    idx = Index()
    idx.add_document("d1", "the quick brown fox jumps over the lazy dog")
    idx.add_document("d2", "the quick blue hare runs fast")
    idx.add_document("d3", "机器学习 是 人工智能 的 分支")
    idx.add_document("d4", "深度学习 是 机器学习 的 分支")
    idx.add_document("d5", "quick quick quick x brown")
    return idx


class TestBasicQueries(unittest.TestCase):
    def setUp(self):
        self.engine = Engine(build_index())

    def test_term(self):
        r = self.engine.search(Term("quick"))
        self.assertFalse(r.incomplete)
        self.assertEqual(r.total_hits, 3)
        self.assertEqual([d for d, _ in r.hits], ["d5", "d1", "d2"])
        self.assertEqual(r.hits[0][1], 3.0)  # 词频打分

    def test_term_cjk(self):
        r = self.engine.search(Term("机"))
        self.assertEqual({d for d, _ in r.hits}, {"d3", "d4"})

    def test_and(self):
        r = self.engine.search(And(Term("quick"), Term("brown")))
        self.assertEqual({d for d, _ in r.hits}, {"d1", "d5"})

    def test_or(self):
        r = self.engine.search(Or(Term("hare"), Term("dog")))
        self.assertEqual({d for d, _ in r.hits}, {"d1", "d2"})

    def test_not(self):
        r = self.engine.search(Not(Term("quick")))
        self.assertEqual({d for d, _ in r.hits}, {"d3", "d4"})

    def test_phrase(self):
        r = self.engine.search(Phrase(["quick", "brown", "fox"]))
        self.assertEqual([d for d, _ in r.hits], ["d1"])

    def test_phrase_cjk(self):
        r = self.engine.search(Phrase(["机", "器", "学", "习"]))
        self.assertEqual({d for d, _ in r.hits}, {"d3", "d4"})

    def test_phrase_no_false_positive(self):
        # d5 有 quick 和 brown 但不相邻
        r = self.engine.search(Phrase(["quick", "brown"]))
        self.assertEqual([d for d, _ in r.hits], ["d1"])

    def test_fuzzy(self):
        r = self.engine.search(Fuzzy("quik", max_edits=1))
        self.assertEqual({d for d, _ in r.hits}, {"d1", "d2", "d5"})

    def test_fuzzy_zero_edits_equals_term(self):
        r = self.engine.search(Fuzzy("quick", max_edits=0))
        self.assertEqual(r.total_hits, 3)

    def test_nested_boolean(self):
        q = And(Or(Term("fox"), Term("hare")), Not(Term("blue")))
        r = self.engine.search(q)
        self.assertEqual([d for d, _ in r.hits], ["d1"])


if __name__ == "__main__":
    unittest.main()
