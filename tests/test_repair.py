import unittest

from parserlib import Origin, PredictiveParser, RepairEngine
from parserlib import demolang
from parserlib.repair import DELETE, INSERT, SUBSTITUTE


def strict_ok(tokens):
    parser = PredictiveParser(demolang.program_grammar(), recover=False)
    return parser.parse(list(tokens)).ok


class RepairTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.grammar = demolang.program_grammar()
        cls.engine = RepairEngine(cls.grammar, budget=2, max_candidates=10_000)

    def repair(self, kinds, budget=2):
        engine = self.engine if budget == 2 else RepairEngine(
            self.grammar, budget=budget, max_candidates=10_000
        )
        return engine.repair(demolang.tokens_from_kinds(kinds))

    def test_valid_input_needs_no_repair(self):
        forest = self.repair("id = num ;")
        self.assertEqual(forest.min_cost, 0)
        self.assertEqual(len(forest.candidates), 1)
        self.assertEqual(forest.candidates[0].edits, ())

    def test_missing_semicolon_inserted(self):
        forest = self.repair("id = num")
        self.assertEqual(forest.min_cost, 1)
        inserts = [
            c for c in forest.candidates
            if len(c.edits) == 1 and c.edits[0].op == INSERT
            and c.edits[0].kind == ";" and c.edits[0].position == 3
        ]
        self.assertEqual(len(inserts), 1)
        for candidate in forest.candidates:
            self.assertTrue(strict_ok(candidate.repaired))

    def test_extra_token_deleted(self):
        forest = self.repair("id = num ; ;")
        self.assertEqual(forest.min_cost, 1)
        self.assertTrue(
            all(c.edits[0].op == DELETE for c in forest.candidates)
        )
        self.assertTrue(strict_ok(forest.candidates[0].repaired))

    def test_missing_operator(self):
        forest = self.repair("id num ;")
        self.assertEqual(forest.min_cost, 1)
        self.assertTrue(
            any(
                c.edits[0].op == INSERT and c.edits[0].kind == "="
                for c in forest.candidates
            )
        )

    def test_multiple_equal_cost_candidates_all_returned(self):
        # "id = ;" can be fixed by inserting 'id' OR 'num' at cost 1;
        # the engine must not pretend one of them is unique.
        forest = self.repair("id = ;")
        self.assertEqual(forest.min_cost, 1)
        self.assertGreaterEqual(len(forest.candidates), 2)
        inserted_kinds = {
            c.edits[0].kind for c in forest.candidates if c.edits[0].op == INSERT
        }
        self.assertIn("id", inserted_kinds)
        self.assertIn("num", inserted_kinds)
        costs = {c.cost for c in forest.candidates}
        self.assertEqual(costs, {1})

    def test_budget_prunes(self):
        forest = self.repair("id = num", budget=0)
        self.assertIsNone(forest.min_cost)
        self.assertEqual(forest.candidates, [])

    def test_cost_two_repair(self):
        forest = self.repair("id num")
        self.assertEqual(forest.min_cost, 2)
        for candidate in forest.candidates:
            self.assertTrue(strict_ok(candidate.repaired))

    def test_inserted_token_identity_and_mapping(self):
        original = demolang.tokens_from_kinds("id = num")
        original_uids = {t.uid for t in original}
        forest = self.engine.repair(original)
        candidate = next(
            c for c in forest.candidates
            if c.edits and c.edits[0].op == INSERT
        )
        repaired = [t for t in candidate.repaired]
        inserted = [t for t in repaired if t.origin is Origin.INSERTED]
        self.assertEqual(len(inserted), 1)
        self.assertNotIn(inserted[0].uid, original_uids)
        self.assertEqual(inserted[0].span, (3, 3))  # zero-width at insert point
        kept = [t for t in repaired if t.origin is Origin.ORIGINAL]
        self.assertEqual({t.uid for t in kept}, original_uids)
        # The repaired stream re-parses and keeps the source mapping.
        parser = PredictiveParser(self.grammar, recover=False)
        result = parser.parse(list(candidate.repaired))
        self.assertTrue(result.ok)
        mapping = result.root.source_map()
        self.assertIn((";", (3, 3), "inserted"), mapping)
        self.assertIn(("id", (0, 1), "original"), mapping)

    def test_substituted_token_identity(self):
        # "id = num + print ;" is fixed at cost 1 only by substituting the
        # keyword 'print' with an expression-starting terminal.
        original = demolang.tokens_from_kinds("id = num + print ;")
        forest = self.engine.repair(original)
        self.assertEqual(forest.min_cost, 1)
        sub = [
            c for c in forest.candidates
            if c.edits and c.edits[0].op == SUBSTITUTE
        ]
        self.assertTrue(sub)
        self.assertTrue(
            all(c.edits[0].op == SUBSTITUTE for c in forest.candidates)
        )
        token = [t for t in sub[0].repaired if t.origin is Origin.SUBSTITUTED][0]
        self.assertNotIn(token.uid, {t.uid for t in original})
        self.assertEqual(token.span, original[4].span)

    def test_shared_forest_structure(self):
        # Two repairs ("insert id" / "insert num") share the whole zero-cost
        # prefix of the search graph: the forest is a DAG, not a tree.
        forest = self.repair("id = ;")
        self.assertGreaterEqual(len(forest.candidates), 2)
        first, second = forest.candidates[0], forest.candidates[1]
        common = 0
        for a, b in zip(first.path, second.path):
            if a != b:
                break
            common += 1
        self.assertGreater(common, 3)  # long shared prefix of forest nodes
        total_path_nodes = sum(len(c.path) for c in forest.candidates)
        self.assertLess(forest.node_count, total_path_nodes)
        self.assertFalse(forest.truncated)


if __name__ == "__main__":
    unittest.main()
