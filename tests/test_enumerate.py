import itertools
import random
import unittest

from errparse.demo_grammar import make_parser
from errparse.enumerate_edits import min_accept_cost
from errparse.repair import minimal_repairs
from errparse.tokens import eof_token, source_token

ALPHABET = ("ID", "NUM", "ASSIGN", "SEMI", "PLUS",
            "PRINT", "IF", "LPAREN", "RPAREN")


def toks(types):
    return [source_token(t, t, i) for i, t in enumerate(types)] + \
        [eof_token(len(types))]


class EnumerateReferenceTest(unittest.TestCase):
    """Cross-check minimal repair costs against brute-force enumeration."""

    @classmethod
    def setUpClass(cls):
        cls.parser = make_parser()

    def check(self, types, budget):
        tokens = toks(types)
        cost, _ = min_accept_cost(tokens, self.parser, ALPHABET, budget)
        forest = minimal_repairs(tokens, self.parser, budget=budget)
        if cost is None:
            self.assertIsNone(forest, types)
        else:
            self.assertIsNotNone(forest, types)
            self.assertEqual(forest.cost, cost, types)

    def test_exhaustive_short_strings(self):
        for n in (0, 1, 2):
            for types in itertools.product(ALPHABET, repeat=n):
                self.check(list(types), budget=2)

    def test_random_longer_strings(self):
        rng = random.Random(20261002)
        for _ in range(30):
            types = [rng.choice(ALPHABET) for _ in range(4)]
            self.check(types, budget=2)

    def test_witnesses_are_accepted(self):
        from errparse.enumerate_edits import accepts
        cost, witnesses = min_accept_cost(toks(["ID", "PLUS", "SEMI"]),
                                          self.parser, ALPHABET, 3)
        self.assertIsNotNone(cost)
        self.assertTrue(all(accepts(w, self.parser) for w in witnesses))


if __name__ == "__main__":
    unittest.main()
