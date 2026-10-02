import unittest

from errparse.demo_grammar import lex, make_parser
from errparse.incremental import IncrementalParser

SRC = "a = 1 ; b = 2 ; c = 3 ; d = 4 ;"


class IncrementalTest(unittest.TestCase):
    def setUp(self):
        self.inc = IncrementalParser(make_parser(), "stmt")
        self.r1 = self.inc.parse(lex(SRC))

    def test_initial_parse(self):
        self.assertEqual(len(self.r1.blocks), 4)
        self.assertTrue(self.r1.ok)
        self.assertEqual(self.r1.reparsed, 4)

    def test_value_change_invalidates_one_block(self):
        r2 = self.inc.update(lex("a = 1 ; b = 99 ; c = 3 ; d = 4 ;"))
        self.assertEqual((r2.reused, r2.reparsed), (3, 1))
        self.assertIs(r2.blocks[0].tree, self.r1.blocks[0].tree)
        self.assertIs(r2.blocks[2].tree, self.r1.blocks[2].tree)
        self.assertIs(r2.blocks[3].tree, self.r1.blocks[3].tree)
        self.assertIsNot(r2.blocks[1].tree, self.r1.blocks[1].tree)
        self.assertTrue(r2.ok)

    def test_broken_block_does_not_swallow_followers(self):
        r2 = self.inc.update(lex("a = 1 ; b = ; c = 3 ; d = 4 ;"))
        self.assertEqual(len(r2.blocks), 4)
        self.assertFalse(r2.ok)
        self.assertIs(r2.blocks[2].tree, self.r1.blocks[2].tree)
        self.assertIs(r2.blocks[3].tree, self.r1.blocks[3].tree)
        types = [leaf.token.type for leaf in r2.blocks[2].tree.iter_tokens()]
        self.assertEqual(types, ["ID", "ASSIGN", "NUM", "SEMI"])

    def test_insert_statement_in_middle(self):
        r2 = self.inc.update(lex("a = 1 ; q = 9 ; b = 2 ; c = 3 ; d = 4 ;"))
        self.assertEqual(len(r2.blocks), 5)
        self.assertTrue(r2.ok)
        self.assertGreaterEqual(r2.reused, 3)
        self.assertLessEqual(r2.reparsed, 2)

    def test_delete_statement(self):
        r2 = self.inc.update(lex("a = 1 ; c = 3 ; d = 4 ;"))
        self.assertEqual(len(r2.blocks), 3)
        self.assertTrue(r2.ok)
        self.assertGreaterEqual(r2.reused, 2)


if __name__ == "__main__":
    unittest.main()
