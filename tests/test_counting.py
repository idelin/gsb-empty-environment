"""Counting tests: brute-force cross-checks, big lengths, cache invalidation."""

import unittest

from automata import Automaton, Counter, ProductAutomaton

from .brute import brute_words, random_product


def no_consecutive_ones():
    """DFA over {0,1} accepting words without '11'; count(n) = F(n+2)."""
    return Automaton(
        alphabet="01",
        states=["last0", "last1"],
        initial="last0",
        accepting={"last0", "last1"},
        transitions={
            ("last0", "0"): "last0",
            ("last0", "1"): "last1",
            ("last1", "0"): "last0",
        },
    )


def fib_pair(n):
    """Fast doubling: (F(n), F(n+1)) with F(0)=0, F(1)=1."""
    if n == 0:
        return 0, 1
    a, b = fib_pair(n >> 1)
    c = a * (2 * b - a)
    d = a * a + b * b
    return (d, c + d) if n & 1 else (c, d)


def fib_mod(n, m):
    a, b = 0, 1
    for bit in bin(n)[2:]:
        c = a * ((2 * b - a) % m) % m
        d = (a * a + b * b) % m
        a, b = (d, (c + d) % m) if bit == "1" else (c, d)
    return a


class TestBruteForceCrossCheck(unittest.TestCase):
    def test_random_intersections_match_brute_force(self):
        for seed in range(30):
            product = random_product(seed)
            counter = Counter(product)
            for lo, hi in [(0, 0), (0, 6), (1, 5), (2, 4), (3, 6), (5, 6)]:
                expected = len(brute_words(product, lo, hi))
                self.assertEqual(counter.count(lo, hi), expected,
                                 f"seed={seed} [{lo},{hi}]")

    def test_mod_mode_matches_exact_mode(self):
        modulus = 1_000_000_007
        for seed in range(10):
            product = random_product(seed)
            exact = Counter(product)
            mod = Counter(product, mode="mod", modulus=modulus)
            for lo, hi in [(0, 6), (2, 5), (4, 6)]:
                self.assertEqual(mod.count(lo, hi),
                                 exact.count(lo, hi) % modulus)

    def test_empty_string_requires_all_constraints(self):
        accepts_empty = Automaton("a", [0], 0, {0}, {})
        rejects_empty = Automaton("a", [0], 0, set(), {})
        both = ProductAutomaton([accepts_empty, rejects_empty])
        self.assertEqual(Counter(both).count(0, 0), 0)
        rejects_empty.set_accept(0, True)  # now both accept ''
        self.assertEqual(Counter(both).count(0, 0), 1)


class TestLargeLengths(unittest.TestCase):
    def test_exact_big_integer_count(self):
        product = ProductAutomaton([no_consecutive_ones()])
        counter = Counter(product)
        n = 500
        self.assertEqual(counter.count_length(n), fib_pair(n + 2)[0])

    def test_interval_count_matches_iterative_sum(self):
        product = ProductAutomaton([no_consecutive_ones()])
        counter = Counter(product)
        total = sum(fib_pair(n + 2)[0] for n in range(0, 121))
        self.assertEqual(counter.count(0, 120), total)
        part = sum(fib_pair(n + 2)[0] for n in range(37, 121))
        self.assertEqual(counter.count(37, 120), part)

    def test_mod_count_huge_length(self):
        modulus = 1_000_000_007
        product = ProductAutomaton([no_consecutive_ones()])
        counter = Counter(product, mode="mod", modulus=modulus)
        n = 10**15
        self.assertEqual(counter.count_length(n), fib_mod(n + 2, modulus))

    def test_power_blocks_are_reused(self):
        product = ProductAutomaton([no_consecutive_ones()])
        counter = Counter(product)
        counter.count_length(10)
        blocks = counter._blocks
        counter.count_length(10**6)  # extends but reuses the same block object
        self.assertIs(counter._blocks, blocks)
        self.assertGreaterEqual(len(blocks.power), 20)


class TestCacheInvalidation(unittest.TestCase):
    def test_mutation_invalidates_cached_blocks(self):
        aut = no_consecutive_ones()
        product = ProductAutomaton([aut])
        counter = Counter(product)
        before = counter.count(0, 5)
        aut.add_transition("last1", "1", "last1")  # allow '11' now
        after = counter.count(0, 5)
        self.assertNotEqual(before, after)
        fresh = Counter(ProductAutomaton([aut])).count(0, 5)
        self.assertEqual(after, fresh)

    def test_accepting_change_invalidates(self):
        aut = no_consecutive_ones()
        product = ProductAutomaton([aut])
        counter = Counter(product)
        self.assertEqual(counter.count(0, 0), 1)  # '' accepted
        aut.set_accept("last0", False)
        aut.set_accept("last1", True)
        self.assertEqual(counter.count(0, 0), 0)
        self.assertEqual(counter.count(1, 1), 1)  # only '1'


if __name__ == "__main__":
    unittest.main()
