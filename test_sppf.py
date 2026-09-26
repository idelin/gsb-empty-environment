"""Unit tests for sppf.py.  Run standalone:  python3.11 test_sppf.py
or via the module runner:                  python3.11 -m unittest test_sppf -v
"""

import unittest

from sppf import Grammar, count_trees, parse, sample_trees


def catalan(n):
    # catalan(k) = number of binary bracketings of k+1 leaves
    if n == 0:
        return 1
    return sum(catalan(i) * catalan(n - 1 - i) for i in range(n))


class TestAmbiguousGrammar(unittest.TestCase):
    """S -> S S | 'a' : the classic ambiguous, left-recursive grammar."""

    def setUp(self):
        self.g = Grammar({"a"}, {"S"},
                         [("S", ("S", "S")), ("S", ("a",))], "S")

    def test_aaa_accepts_and_keeps_ambiguity(self):
        res = parse(self.g, "aaa")
        self.assertTrue(res.accepted)
        self.assertEqual(res.farthest, 3)
        self.assertEqual(count_trees(res.root), 2)  # (a(aa)) and ((aa)a)

    def test_enumerated_trees_match_count(self):
        res = parse(self.g, "aaa")
        self.assertEqual(len(sample_trees(res.root, 100)), 2)

    def test_catalan_counts_for_longer_inputs(self):
        for length in (1, 2, 3, 4, 5, 6):
            res = parse(self.g, "a" * length)
            self.assertTrue(res.accepted)
            self.assertEqual(count_trees(res.root), catalan(length - 1))

    def test_forest_stays_compact_while_trees_explode(self):
        res = parse(self.g, "a" * 10)
        self.assertEqual(count_trees(res.root), catalan(9))  # 4862 trees
        self.assertLess(res.node_count, 300)                 # shared, not enumerated

    def test_distinct_proof_edges_survive_state_dedup(self):
        # The symbol node (S, 0, 3) is reached through a single Earley item,
        # yet it must carry TWO packed families (the two bracketings).
        res = parse(self.g, "aaa")
        found = []

        def walk(node):
            if node.label == "S" and (node.start, node.end) == (0, 3):
                found.append(node)
            for family in node.families:
                for child in family:
                    walk(child)

        walk(res.root)
        self.assertEqual(len(found), 1)          # node is shared (deduped)
        self.assertEqual(len(found[0].families), 2)  # but both edges are kept

    def test_rejection(self):
        res = parse(self.g, "")
        self.assertFalse(res.accepted)
        self.assertEqual(res.farthest, 0)


class TestLeftRecursion(unittest.TestCase):
    def test_pure_left_recursion(self):
        g = Grammar({"a"}, {"S"}, [("S", ("S", "a")), ("S", ("a",))], "S")
        res = parse(g, "aaaa")
        self.assertTrue(res.accepted)
        self.assertEqual(count_trees(res.root), 1)

    def test_left_and_right_recursion_terminate(self):
        g = Grammar({"a"}, {"S"},
                    [("S", ("S", "S")), ("S", ("a",))], "S")
        # would loop forever in a naive recursive-descent parser
        res = parse(g, "aaaaa")
        self.assertTrue(res.accepted)

    def test_ambiguous_expression_grammar(self):
        g = Grammar({"n", "+"}, {"E"},
                    [("E", ("E", "+", "E")), ("E", ("n",))], "E")
        res = parse(g, ["n", "+", "n", "+", "n"])
        self.assertTrue(res.accepted)
        self.assertEqual(count_trees(res.root), 2)


class TestEpsilon(unittest.TestCase):
    def setUp(self):
        # S -> A B ; A -> 'a' | eps ; B -> 'b' | eps
        self.g = Grammar({"a", "b"}, {"S", "A", "B"},
                         [("S", ("A", "B")),
                          ("A", ("a",)), ("A", ()),
                          ("B", ("b",)), ("B", ())], "S")

    def test_empty_input_accepted_by_nullable_grammar(self):
        res = parse(self.g, "")
        self.assertTrue(res.accepted)
        self.assertEqual(res.farthest, 0)
        self.assertEqual(count_trees(res.root), 1)

    def test_nullable_combinations(self):
        for text in ("a", "b", "ab"):
            self.assertTrue(parse(self.g, text).accepted, text)

    def test_empty_input_rejected_by_non_nullable_grammar(self):
        g = Grammar({"a"}, {"S"}, [("S", ("a",))], "S")
        res = parse(g, "")
        self.assertFalse(res.accepted)
        self.assertEqual(res.farthest, 0)

    def test_nullable_left_corner_finite(self):
        # S -> S 'a' | A ; A -> 'b' | eps   (left recursion fed by epsilon)
        g = Grammar({"a", "b"}, {"S", "A"},
                    [("S", ("S", "a")), ("S", ("A",)),
                     ("A", ("b",)), ("A", ())], "S")
        for text in ("a", "aa", "b", "ba"):
            res = parse(g, text)
            self.assertTrue(res.accepted, text)
            self.assertEqual(count_trees(res.root), 1, text)

    def test_finite_epsilon_ambiguity(self):
        # S -> A A ; A -> 'a' | eps : "a" has exactly two derivations.
        g = Grammar({"a"}, {"S", "A"},
                    [("S", ("A", "A")), ("A", ("a",)), ("A", ())], "S")
        res = parse(g, "a")
        self.assertTrue(res.accepted)
        self.assertEqual(count_trees(res.root), 2)
        res = parse(g, "")
        self.assertTrue(res.accepted)
        self.assertEqual(count_trees(res.root), 1)

    def test_nullable_in_recursive_cycle_is_infinitely_ambiguous(self):
        # S -> S A | 'a' ; A -> 'b' | eps : "a" has infinitely many
        # derivations (S->SA applied k times, all A's -> eps).  The SPPF
        # is then cyclic, which count_trees reports instead of hanging.
        g = Grammar({"a", "b"}, {"S", "A"},
                    [("S", ("S", "A")), ("S", ("a",)),
                     ("A", ("b",)), ("A", ())], "S")
        res = parse(g, "a")
        self.assertTrue(res.accepted)
        with self.assertRaises(ValueError):
            count_trees(res.root)

    def test_hidden_left_recursion_through_epsilon(self):
        # A -> B A | 'x' ; B -> eps : nullable left corner, also infinitely
        # ambiguous, and recognition must still terminate and accept.
        g = Grammar({"x"}, {"S", "A", "B"},
                    [("S", ("A",)),
                     ("A", ("B", "A")), ("A", ("x",)),
                     ("B", ())], "S")
        res = parse(g, "x")
        self.assertTrue(res.accepted)
        with self.assertRaises(ValueError):
            count_trees(res.root)


class TestFarthestFailure(unittest.TestCase):
    def test_farthest_position_and_expected_terminals(self):
        g = Grammar({"a", "b", "c"}, {"S"}, [("S", ("a", "b", "c"))], "S")
        res = parse(g, "aba")
        self.assertFalse(res.accepted)
        self.assertEqual(res.farthest, 2)
        self.assertEqual(res.expected, frozenset({"c"}))

    def test_failure_at_first_token(self):
        g = Grammar({"a"}, {"S"}, [("S", ("a",))], "S")
        res = parse(g, "")  # nothing consumed at all
        self.assertFalse(res.accepted)
        self.assertEqual(res.farthest, 0)

    def test_farthest_with_nullable_prefix(self):
        # S -> A B ; A -> 'a' | eps ; B -> 'b' | eps ; input 'ba'
        g = Grammar({"a", "b"}, {"S", "A", "B"},
                    [("S", ("A", "B")),
                     ("A", ("a",)), ("A", ()),
                     ("B", ("b",)), ("B", ())], "S")
        res = parse(g, "ba")
        self.assertFalse(res.accepted)
        self.assertEqual(res.farthest, 1)


class TestExplicitAlphabet(unittest.TestCase):
    def test_token_outside_alphabet_rejected(self):
        g = Grammar({"a"}, {"S"}, [("S", ("a",))], "S")
        with self.assertRaises(ValueError):
            parse(g, "ab")

    def test_unknown_symbol_in_production_rejected(self):
        with self.assertRaises(ValueError):
            Grammar({"a"}, {"S"}, [("S", ("a", "z"))], "S")

    def test_terminal_nonterminal_overlap_rejected(self):
        with self.assertRaises(ValueError):
            Grammar({"a"}, {"a"}, [("a", ())], "a")

    def test_multi_character_tokens(self):
        g = Grammar({"if", "then", "id"}, {"S"},
                    [("S", ("if", "id", "then", "id"))], "S")
        res = parse(g, ["if", "id", "then", "id"])
        self.assertTrue(res.accepted)


class TestDedupNoDuplicateEdges(unittest.TestCase):
    def test_unambiguous_grammar_yields_exactly_one_tree(self):
        # S -> A A ; A -> 'a'  (same Earley states hit repeatedly)
        g = Grammar({"a"}, {"S", "A"},
                    [("S", ("A", "A")), ("A", ("a",))], "S")
        res = parse(g, "aa")
        self.assertTrue(res.accepted)
        self.assertEqual(count_trees(res.root), 1)

    def test_no_duplicate_families_anywhere(self):
        g = Grammar({"a"}, {"S"},
                    [("S", ("S", "S")), ("S", ("a",))], "S")
        res = parse(g, "aaaa")
        seen = set()

        def walk(node):
            if id(node) in seen:
                return  # shared node: the forest is a DAG, not a tree
            seen.add(id(node))
            families = list(node.families)
            self.assertEqual(len(families), len(set(families)))
            for family in families:
                for child in family:
                    walk(child)

        walk(res.root)
        # interned nodes include partial-derivation nodes unreachable from
        # the root, so the reachable set is a subset of all interned nodes
        self.assertLessEqual(len(seen), res.node_count)


if __name__ == "__main__":
    unittest.main(verbosity=2)
