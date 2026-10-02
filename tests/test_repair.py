import unittest

from errparse.demo_grammar import lex, make_parser
from errparse.repair import apply_edits, minimal_repairs
from errparse.tokens import INSERTED, SOURCE


class RepairTest(unittest.TestCase):
    def setUp(self):
        self.parser = make_parser()

    def test_valid_input_cost_zero(self):
        forest = minimal_repairs(lex("x = 1 ;"), self.parser, budget=2)
        self.assertEqual(forest.cost, 0)
        self.assertEqual(forest.enumerate(), [()])

    def test_all_equal_cost_candidates_kept(self):
        forest = minimal_repairs(lex("y = x + ;"), self.parser, budget=3)
        self.assertEqual(forest.cost, 1)
        scripts = forest.enumerate(limit=50)
        self.assertEqual(len(scripts), forest.count())
        described = {tuple(e.describe() for e in s) for s in scripts}
        self.assertIn(("delete PLUS at position 3",), described)
        self.assertIn(("insert ID before position 4",), described)
        self.assertIn(("insert NUM before position 4",), described)
        self.assertGreaterEqual(len(scripts), 3)

    def test_every_candidate_actually_repairs(self):
        tokens = lex("y = x + ;")
        forest = minimal_repairs(tokens, self.parser, budget=3)
        self.assertGreater(forest.count(), 0)
        for script in forest.enumerate(limit=20):
            repaired = apply_edits(tokens, script)
            self.assertTrue(self.parser.parse(repaired).ok, script)

    def test_identity_and_source_mapping(self):
        tokens = lex("y = x + ;")
        forest = minimal_repairs(tokens, self.parser, budget=3)
        script = next(s for s in forest.enumerate()
                      if any(e.kind == "insert" for e in s))
        repaired = apply_edits(tokens, script)
        orig_uids = {t.uid for t in tokens}
        src_pos = {t.uid: t.pos for t in tokens}
        saw_inserted = False
        for t in repaired:
            if t.origin == SOURCE:
                self.assertIn(t.uid, orig_uids)
                self.assertEqual(t.pos, src_pos[t.uid])
            elif t.origin == INSERTED:
                saw_inserted = True
                self.assertNotIn(t.uid, orig_uids)
        self.assertTrue(saw_inserted)

    def test_budget_respected(self):
        self.assertIsNone(minimal_repairs(lex("= = ="), self.parser, budget=1))
        forest = minimal_repairs(lex("= = ="), self.parser, budget=3)
        self.assertIsNotNone(forest)
        self.assertLessEqual(forest.cost, 3)


if __name__ == "__main__":
    unittest.main()
