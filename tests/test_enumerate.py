"""Budgeted resumable enumeration: checkpoints, budgets, invalidation."""

import unittest

from automata import Counter, LexEnumerator, ProductAutomaton

from .brute import brute_words, random_automaton, random_product


class TestEnumeration(unittest.TestCase):
    def test_full_enumeration_matches_brute_force(self):
        for seed in range(10):
            product = random_product(seed)
            counter = Counter(product)
            words = brute_words(product, 0, 6)
            enum = LexEnumerator(counter, 0, 6)
            self.assertEqual(list(enum), words)

    def test_budgeted_batches_with_resume(self):
        product = random_product(3)
        counter = Counter(product)
        lo, hi = 0, 6
        expected = brute_words(product, lo, hi)
        enum = LexEnumerator(counter, lo, hi)
        got = []
        while True:
            batch, done = enum.take(3)
            got.extend(batch)
            if done:
                break
            # checkpoint / resume between batches
            enum = LexEnumerator.resume(counter, lo, hi, enum.checkpoint())
        self.assertEqual(got, expected)

    def test_budget_exhaustion_is_not_nonexistence(self):
        from automata import Automaton
        # all words over 'ab': 2^(n+1)-1 words of length <= 6
        open_aut = Automaton("ab", [0], 0, {0}, {(0, "a"): 0, (0, "b"): 0})
        product = ProductAutomaton([open_aut])
        counter = Counter(product)
        total = counter.count(0, 6)
        self.assertGreater(total, 2)
        enum = LexEnumerator(counter, 0, 6)
        batch, done = enum.take(2)
        self.assertEqual(len(batch), 2)
        self.assertFalse(done)  # budget spent: NOT a claim of non-existence
        rest, done = enum.take(total)
        self.assertTrue(done)
        self.assertEqual(len(batch) + len(rest), total)

    def test_zero_budget(self):
        product = random_product(5)
        counter = Counter(product)
        enum = LexEnumerator(counter, 0, 4)
        words, done = enum.take(0)
        self.assertEqual(words, [])
        self.assertFalse(done)

    def test_done_only_after_real_exhaustion(self):
        product = random_product(6)
        counter = Counter(product)
        total = counter.count(0, 6)
        enum = LexEnumerator(counter, 0, 6)
        batch, done = enum.take(total)
        self.assertEqual(len(batch), total)
        self.assertFalse(done)  # exactly budget-sized: not yet known
        batch, done = enum.take(1)
        self.assertEqual(batch, [])
        self.assertTrue(done)

    def test_checkpoint_binds_automaton_version(self):
        product = random_product(7)
        counter = Counter(product)
        enum = LexEnumerator(counter, 0, 6)
        enum.take(2)
        cp = enum.checkpoint()
        # resume against unchanged automata works
        LexEnumerator.resume(counter, 0, 6, cp)
        # mutating any constraint invalidates the checkpoint
        product.automata[0].set_accept(product.automata[0].initial, True)
        with self.assertRaises(ValueError):
            LexEnumerator.resume(counter, 0, 6, cp)

    def test_checkpoint_binds_alphabet(self):
        product = random_product(8, alphabet="ab")
        counter = Counter(product)
        enum = LexEnumerator(counter, 0, 6)
        enum.take(1)
        cp = enum.checkpoint()
        other = ProductAutomaton([random_automaton(__import__("random").Random(1), "abc", 2)])
        other_counter = Counter(other)
        with self.assertRaises(ValueError):
            LexEnumerator.resume(other_counter, 0, 6, cp)

    def test_enumeration_requires_exact_mode(self):
        product = random_product(9)
        counter = Counter(product, mode="mod", modulus=101)
        with self.assertRaises(ValueError):
            LexEnumerator(counter, 0, 4)


if __name__ == "__main__":
    unittest.main()
