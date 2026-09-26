"""k-th word / ranking tests: round-trips and brute-force cross-checks."""

import unittest

from automata import Counter

from .brute import brute_words, random_product


class TestKthAndRank(unittest.TestCase):
    def test_kth_matches_brute_force_order(self):
        for seed in range(20):
            product = random_product(seed)
            counter = Counter(product)
            for lo, hi in [(0, 6), (1, 5), (2, 4)]:
                words = brute_words(product, lo, hi)
                for k, word in enumerate(words):
                    self.assertEqual(counter.kth(k, lo, hi), word,
                                     f"seed={seed} [{lo},{hi}] k={k}")

    def test_rank_matches_brute_force_order(self):
        for seed in range(20):
            product = random_product(seed)
            counter = Counter(product)
            for lo, hi in [(0, 6), (3, 6)]:
                words = brute_words(product, lo, hi)
                for k, word in enumerate(words):
                    self.assertEqual(counter.rank(word, lo, hi), k,
                                     f"seed={seed} [{lo},{hi}] word={word!r}")

    def test_unrank_recovers_k(self):
        """Generating a word from k and ranking it must return k."""
        for seed in range(20):
            product = random_product(seed)
            counter = Counter(product)
            lo, hi = 0, 6
            total = counter.count(lo, hi)
            for k in range(total):
                word = counter.kth(k, lo, hi)
                self.assertEqual(counter.rank(word, lo, hi), k)

    def test_kth_out_of_range(self):
        product = random_product(0)
        counter = Counter(product)
        total = counter.count(0, 4)
        with self.assertRaises(IndexError):
            counter.kth(total, 0, 4)
        with self.assertRaises(IndexError):
            counter.kth(-1, 0, 4)

    def test_rank_rejects_words_outside_language(self):
        product = random_product(1)
        counter = Counter(product)
        counted = set(brute_words(product, 0, 4))
        candidate = "bbbbbb"
        self.assertNotIn(candidate, counted)  # length outside interval
        with self.assertRaises(ValueError):
            counter.rank(candidate, 0, 4)

    def test_empty_string_selection(self):
        from automata import Automaton, ProductAutomaton
        # both constraints accept '' -> '' is the very first word
        open_aut = Automaton("a", [0], 0, {0}, {(0, "a"): 0})
        product = ProductAutomaton([open_aut, open_aut])
        counter = Counter(product)
        self.assertEqual(counter.kth(0, 0, 5), "")
        self.assertEqual(counter.rank("", 0, 5), 0)
        # one constraint rejects '' (but accepts longer words)
        closed_aut = Automaton("a", [0, 1], 0, {1},
                               {(0, "a"): 1, (1, "a"): 1})
        product = ProductAutomaton([open_aut, closed_aut])
        counter = Counter(product)
        self.assertEqual(counter.count(0, 0), 0)
        self.assertEqual(counter.kth(0, 0, 5), "a")

    def test_mod_mode_cannot_select(self):
        product = random_product(2)
        counter = Counter(product, mode="mod", modulus=97)
        with self.assertRaises(ValueError):
            counter.kth(0, 0, 4)
        with self.assertRaises(ValueError):
            counter.rank("ab", 0, 4)


if __name__ == "__main__":
    unittest.main()
