import unittest

from errparse.demo_grammar import lex, make_parser
from errparse.tokens import EOF_SYMBOL, INSERTED, SOURCE


def stmt_texts(tree):
    """Token (type, value) sequence of each stmt subtree."""
    out = []

    def walk(node):
        if node.symbol == "stmt" and node.kind == "rule":
            out.append([(leaf.token.type, leaf.token.value)
                        for leaf in node.iter_tokens()])
        for c in node.children:
            walk(c)

    walk(tree)
    return out


def error_tokens(tree):
    out = []

    def walk(node):
        if node.kind == "error":
            out.extend(leaf.token for leaf in node.iter_tokens())
        for c in node.children:
            walk(c)

    walk(tree)
    return out


class ParserTest(unittest.TestCase):
    def setUp(self):
        self.parser = make_parser()

    def test_clean_parse(self):
        res = self.parser.parse(lex("x = 1 + 2 ; print x ;"))
        self.assertTrue(res.ok)
        types = [leaf.token.type for leaf in res.tree.iter_tokens()]
        self.assertEqual(types, ["ID", "ASSIGN", "NUM", "PLUS", "NUM", "SEMI",
                                 "PRINT", "ID", "SEMI"])

    def test_source_mapping_preserved(self):
        toks = lex("x = 1 ;")
        res = self.parser.parse(toks)
        leaves = [leaf.token for leaf in res.tree.iter_tokens()]
        self.assertEqual([t.uid for t in leaves], [t.uid for t in toks[:-1]])
        self.assertTrue(all(t.origin == SOURCE for t in leaves))

    def test_empty_production_builds_tree(self):
        res = self.parser.parse(lex(""))
        self.assertTrue(res.ok)
        kinds = []

        def walk(n):
            kinds.append(n.kind)
            for c in n.children:
                walk(c)

        walk(res.tree)
        self.assertIn("epsilon", kinds)

    def test_missing_terminal_reports_expected_set(self):
        res = self.parser.parse(lex("print ( x ;"))
        self.assertEqual(len(res.errors), 1)
        err = res.errors[0]
        self.assertEqual(err.kind, "missing")
        self.assertEqual(err.expected, ("RPAREN",))
        inserted = [leaf.token for leaf in res.tree.iter_tokens()
                    if leaf.token.origin == INSERTED]
        self.assertEqual([t.type for t in inserted], ["RPAREN"])
        self.assertNotEqual(inserted[0].uid, "")

    def test_error_node_keeps_skipped_tokens(self):
        toks = lex(") y = 2 ;")
        res = self.parser.parse(toks)
        self.assertEqual(len(res.errors), 1)
        skipped = error_tokens(res.tree)
        self.assertEqual([t.uid for t in skipped], [toks[0].uid])
        texts = stmt_texts(res.tree)
        self.assertIn([("ID", "y"), ("ASSIGN", "="), ("NUM", "2"), ("SEMI", ";")],
                      texts)

    def test_recovery_does_not_swallow_following_statement(self):
        res = self.parser.parse(lex("x = 1 ; = 3 ; z = 4 ;"))
        texts = stmt_texts(res.tree)
        self.assertIn([("ID", "z"), ("ASSIGN", "="), ("NUM", "4"), ("SEMI", ";")],
                      texts)
        skipped = [t.value for t in error_tokens(res.tree)]
        self.assertEqual(skipped, ["=", "3", ";"])

    def test_if_body_recovers_into_legal_structure(self):
        res = self.parser.parse(lex("if x = 3 ; z = 4 ;"))
        types = [leaf.token.type for leaf in res.tree.iter_tokens()]
        # the legal `z = 4 ;` survives as a consecutive, fully parsed run
        run = ["ID", "ASSIGN", "NUM", "SEMI"]
        self.assertTrue(any(types[k:k + 4] == run
                            for k in range(len(types) - 3)), types)

    def test_many_errors_terminate_positions_unique(self):
        res = self.parser.parse(lex(") ) = ; ( if x ;"))
        self.assertGreaterEqual(len(res.errors), 2)
        positions = [e.pos for e in res.errors]
        self.assertEqual(len(positions), len(set(positions)))

    def test_eof_errors_terminate(self):
        res = self.parser.parse(lex("if ( x"))
        positions = [e.pos for e in res.errors]
        self.assertEqual(len(positions), len(set(positions)))
        self.assertGreaterEqual(len(res.errors), 1)


if __name__ == "__main__":
    unittest.main()
