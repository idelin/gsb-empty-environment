"""Cross-validate the repair engine against brute-force edit enumeration."""
import itertools
import random
import unittest

from parserlib import Grammar, PredictiveParser, RepairEngine
from parserlib.reference import reference_repairs
from parserlib.tokens import Token

ALPHABET = ("x", "y", ";")
RULES = {
    "s": [("item", ";")],
    "item": [("x",), ("y", "x")],
}
GRAMMAR = Grammar("s", RULES, frozenset(ALPHABET))


def to_tokens(kinds):
    return [
        Token(kind=k, text=k, start=i, end=i + 1) for i, k in enumerate(kinds)
    ]


def strict_ok(kinds):
    parser = PredictiveParser(GRAMMAR, recover=False)
    return parser.parse(to_tokens(kinds)).ok


def is_ok(seq):
    return strict_ok(seq)


class ReferenceCrossCheckTest(unittest.TestCase):
    BUDGET = 2

    @classmethod
    def setUpClass(cls):
        cls.engine = RepairEngine(GRAMMAR, budget=cls.BUDGET, max_candidates=100_000)

    def check(self, kinds):
        forest = self.engine.repair(to_tokens(kinds))
        ref_cost, ref_seqs = reference_repairs(
            tuple(kinds), ALPHABET, self.BUDGET, is_ok
        )
        self.assertEqual(
            forest.min_cost, ref_cost, f"min cost mismatch for {kinds}"
        )
        engine_seqs = {c.kind_sequence() for c in forest.candidates}
        self.assertEqual(
            engine_seqs, ref_seqs, f"candidate set mismatch for {kinds}"
        )
        for candidate in forest.candidates:
            self.assertTrue(
                strict_ok(
                    [t.kind for t in candidate.repaired if t.kind != "$"]
                )
            )

    def test_exhaustive_short_strings(self):
        for length in range(0, 4):
            for kinds in itertools.product(ALPHABET, repeat=length):
                self.check(list(kinds))

    def test_random_longer_strings(self):
        rng = random.Random(20261002)
        for _ in range(30):
            length = rng.randint(4, 6)
            kinds = [rng.choice(ALPHABET) for _ in range(length)]
            self.check(kinds)


if __name__ == "__main__":
    unittest.main()
