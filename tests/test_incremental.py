import unittest

from parserlib import IncrementalParser, Token
from parserlib import demolang

KINDS = "id = num ; print num ; id = id ; print id ;"


def make_parser():
    parser = IncrementalParser(demolang.statement_grammar())
    tokens = demolang.tokens_from_kinds(KINDS)
    report = parser.load(tokens)
    return parser, report


class IncrementalTest(unittest.TestCase):
    def test_initial_parse_builds_all_blocks(self):
        parser, report = make_parser()
        self.assertEqual(len(parser.blocks), 4)
        self.assertEqual(report.invalidated, [0, 1, 2, 3])
        self.assertEqual(report.reused, [])
        self.assertEqual(report.diagnostics, [])

    def test_edit_invalidates_only_its_block(self):
        parser, _ = make_parser()
        before = [block.tree for block in parser.blocks]
        # Replace the 'num' in statement 1 (token index 2) with a fresh token.
        report = parser.replace(2, Token(kind="num", text="7", start=2, end=3))
        self.assertEqual(report.invalidated, [0])
        self.assertEqual(sorted(report.reused), [1, 2, 3])
        after = [block.tree for block in parser.blocks]
        self.assertIsNot(after[0], before[0])
        for i in (1, 2, 3):
            self.assertIs(after[i], before[i])  # untouched blocks keep trees
        self.assertEqual(report.diagnostics, [])

    def test_error_does_not_swallow_later_blocks(self):
        parser, _ = make_parser()
        # Break statement 1: replace '=' with '+'.
        report = parser.replace(1, Token(kind="+", text="+", start=1, end=2))
        self.assertEqual(report.invalidated, [0])
        self.assertTrue(parser.blocks[0].diagnostics)
        for i in (1, 2, 3):
            self.assertEqual(
                parser.blocks[i].diagnostics,
                (),
                f"block {i} was corrupted by an earlier error",
            )

    def test_delete_merges_only_neighbouring_blocks(self):
        parser, _ = make_parser()
        before = [block.tree for block in parser.blocks]
        # Delete the ';' ending statement 1: blocks 1+2 merge.
        report = parser.delete(3)
        self.assertEqual(len(parser.blocks), 3)
        self.assertEqual(report.invalidated, [0])
        self.assertEqual(sorted(report.reused), [1, 2])
        self.assertIs(parser.blocks[1].tree, before[2])
        self.assertIs(parser.blocks[2].tree, before[3])
        self.assertTrue(parser.blocks[0].diagnostics)  # merged block is broken
        self.assertEqual(parser.blocks[1].diagnostics, ())
        self.assertEqual(parser.blocks[2].diagnostics, ())

    def test_insert_invalidates_only_touched_block(self):
        parser, _ = make_parser()
        before = [block.tree for block in parser.blocks]
        # Insert a '+' inside statement 3 (before its 'id' RHS, index 10).
        report = parser.insert(10, Token(kind="+", text="+", start=10, end=11))
        self.assertEqual(report.invalidated, [2])
        self.assertEqual(sorted(report.reused), [0, 1, 3])
        self.assertIs(parser.blocks[0].tree, before[0])
        self.assertIs(parser.blocks[1].tree, before[1])
        self.assertIs(parser.blocks[3].tree, before[3])

    def test_repeated_edits_keep_cache_coherent(self):
        parser, _ = make_parser()
        parser.replace(2, Token(kind="num", text="7", start=2, end=3))
        report = parser.replace(2, Token(kind="num", text="41", start=2, end=3))
        self.assertEqual(report.invalidated, [0])
        self.assertEqual(report.diagnostics, [])


if __name__ == "__main__":
    unittest.main()
