"""Unittest suite for the exact alias table (Python 3.11, stdlib only).

Tests are labeled [EXACT] when they prove a property with exact rational
arithmetic (full outcome enumeration or algebraic reconstruction), and
[EMPIRICAL] when they only check statistical agreement on sampled data.
"""

import math
import unittest
from collections import Counter
from decimal import Decimal
from fractions import Fraction

from alias_table import (
    AliasTable,
    AliasTablePublisher,
    SeededRandomSource,
)


def exact_sampled_distribution(table):
    """[EXACT] Distribution from enumerating all random choices."""
    outcomes = table.enumerate_outcomes()
    counts = Counter(outcomes)
    total = len(outcomes)
    return [Fraction(counts.get(i, 0), total) for i in range(len(table.weights))]


class TestConstructionValidation(unittest.TestCase):
    def test_empty_weights_rejected(self):
        with self.assertRaises(ValueError):
            AliasTable([])

    def test_all_zero_weights_rejected(self):
        with self.assertRaises(ValueError):
            AliasTable([0, 0, 0])

    def test_negative_weight_rejected(self):
        for bad in (-1, Fraction(-1, 5), -0.25, Decimal("-2")):
            with self.assertRaises(ValueError, msg=f"{bad!r}"):
                AliasTable([1, bad, 3])

    def test_non_finite_weight_rejected(self):
        for bad in (float("inf"), float("-inf"), float("nan"),
                    Decimal("Infinity"), Decimal("NaN")):
            with self.assertRaises(ValueError, msg=f"{bad!r}"):
                AliasTable([1, bad])

    def test_bool_weight_rejected(self):
        with self.assertRaises(TypeError):
            AliasTable([True, 1])

    def test_accepts_int_fraction_float_decimal_str(self):
        table = AliasTable([1, Fraction(1, 3), 0.5, Decimal("0.25"), "2/5"])
        self.assertEqual(
            table.weights,
            (Fraction(1), Fraction(1, 3), Fraction(1, 2),
             Fraction(1, 4), Fraction(2, 5)),
        )


class TestExactDistribution(unittest.TestCase):
    """[EXACT] proofs via enumeration of all random choices / algebra."""

    def check_exact(self, weights):
        table = AliasTable(weights)
        expected = table.normalized_weights
        # Algebraic: table reconstruction equals normalized weights.
        self.assertEqual(table.probabilities(), expected)
        # Enumerative: every (column, threshold) pair, counted exactly.
        self.assertEqual(exact_sampled_distribution(table), expected)

    def test_small_integer_weights(self):
        self.check_exact([1, 2, 3])

    def test_rational_weights(self):
        self.check_exact([Fraction(1, 2), Fraction(1, 3), Fraction(1, 6), 2])

    def test_weights_with_zeros(self):
        self.check_exact([0, 3, 0, 1, 2])

    def test_tiny_weights_exact(self):
        tiny = Fraction(1, 10**30)
        weights = [tiny, 1, 2, Fraction(1, 10**18)]
        table = AliasTable(weights)
        expected = table.normalized_weights
        # [EXACT] algebraic reconstruction (enumeration infeasible: lcm huge)
        self.assertEqual(table.probabilities(), expected)
        self.assertEqual(expected[0], tiny / (3 + tiny + Fraction(1, 10**18)))

    def test_single_weight_item_always_selected(self):
        table = AliasTable([7])
        source = SeededRandomSource(seed=42)
        self.assertEqual(table.sample(source, 1000), [0] * 1000)
        self.assertEqual(table.probabilities(), [Fraction(1)])

    def test_single_positive_among_zeros_always_selected(self):
        table = AliasTable([0, 0, 5, 0])
        # [EXACT] every possible random choice yields item 2
        self.assertEqual(set(table.enumerate_outcomes()), {2})
        source = SeededRandomSource(seed=7)
        self.assertEqual(table.sample(source, 500), [2] * 500)

    def test_zero_weight_never_selected(self):
        table = AliasTable([4, 0, 1])
        # [EXACT] item 1 occurs in no enumerated outcome
        self.assertNotIn(1, table.enumerate_outcomes())
        self.assertEqual(table.probabilities()[1], 0)

    def test_probabilities_sum_to_one(self):
        for weights in ([1, 2, 3, 4], [Fraction(1, 7)] * 7, [0, 0, 9]):
            table = AliasTable(weights)
            self.assertEqual(sum(table.probabilities()), 1)


class TestSerializationReplay(unittest.TestCase):
    def test_roundtrip_table_identical(self):
        table = AliasTable([Fraction(1, 10**12), 3, 0, Fraction(2, 7)])
        restored = AliasTable.loads(table.dumps())
        self.assertEqual(restored, table)
        self.assertEqual(restored.probabilities(), table.normalized_weights)

    def test_seeded_replay_after_roundtrip(self):
        table = AliasTable([5, 1, Fraction(1, 100), 0, 2])
        restored = AliasTable.loads(table.dumps())
        before = table.sample(SeededRandomSource(seed="replay"), 2000)
        after = restored.sample(SeededRandomSource(seed="replay"), 2000)
        self.assertEqual(before, after)  # [EXACT] same seed, same table

    def test_corrupt_payload_rejected(self):
        table = AliasTable([1, 2, 3])
        data = table.to_dict()
        data["q"][0] = str(table._lcm)  # tamper: column always picks itself
        with self.assertRaises(ValueError):
            AliasTable.from_dict(data)


class TestVersionedPublishing(unittest.TestCase):
    def test_old_session_keeps_old_distribution(self):
        publisher = AliasTablePublisher([1, 1, 1])
        old_session = publisher.open_session()
        self.assertEqual(old_session.version, 1)

        new_version = publisher.update_weights([10, 1, 1])
        self.assertEqual(new_version, 2)
        new_session = publisher.open_session()

        # [EXACT] pinned tables differ and keep their own distributions
        self.assertEqual(old_session.table.probabilities(),
                         [Fraction(1, 3)] * 3)
        self.assertEqual(new_session.table.probabilities(),
                         [Fraction(10, 12), Fraction(1, 12), Fraction(1, 12)])

        # [EXACT] same seed: old session replays the old table's sequence
        old_table = AliasTable([1, 1, 1])
        expected = old_table.sample(SeededRandomSource(seed=99), 500)
        got = old_session.sample(SeededRandomSource(seed=99), 500)
        self.assertEqual(got, expected)

    def test_batch_update_atomicity_of_visible_table(self):
        publisher = AliasTablePublisher([2, 3])
        first = publisher.current_table
        publisher.update_weights([5, 6, 7])
        # previously fetched table object is untouched by the update
        self.assertIsNot(first, publisher.current_table)
        self.assertEqual(first.probabilities(),
                         [Fraction(2, 5), Fraction(3, 5)])
        self.assertEqual(publisher.current_table.probabilities(),
                         [Fraction(5, 18), Fraction(6, 18), Fraction(7, 18)])

    def test_invalid_update_leaves_old_version_published(self):
        publisher = AliasTablePublisher([1, 2])
        with self.assertRaises(ValueError):
            publisher.update_weights([1, -2])
        self.assertEqual(publisher.version, 1)
        self.assertEqual(publisher.current_table.probabilities(),
                         [Fraction(1, 3), Fraction(2, 3)])


class TestBookkeeping(unittest.TestCase):
    def test_seed_and_sample_count_recorded(self):
        publisher = AliasTablePublisher([1, 2, 3])
        session = publisher.open_session()
        source = SeededRandomSource(seed=2024)
        session.sample(source, 10)
        session.sample_one(source)
        self.assertEqual(source.seed, 2024)
        self.assertEqual(session.samples_drawn, 11)
        self.assertEqual(source.draws, 22)  # two integers per sample

    def test_out_of_range_source_rejected(self):
        table = AliasTable([1, 1])

        class BrokenSource:
            def randbelow(self, n):
                return n  # biased / out of range

        with self.assertRaises(ValueError):
            table.sample_one(BrokenSource())


class TestEmpiricalAgreement(unittest.TestCase):
    """[EMPIRICAL] statistical checks only; exact proofs are above."""

    def test_empirical_frequencies_match_within_tolerance(self):
        weights = [1, 2, 3, 4]
        table = AliasTable(weights)
        expected = table.normalized_weights
        trials = 200_000
        source = SeededRandomSource(seed=12345)
        counts = Counter(table.sample(source, trials))
        for i, p in enumerate(expected):
            freq = Fraction(counts.get(i, 0), trials)
            # 5-sigma binomial tolerance, generous to avoid flakiness
            sigma = math.sqrt(float(p) * (1 - float(p)) / trials)
            self.assertAlmostEqual(float(freq), float(p), delta=5 * sigma,
                                   msg=f"[EMPIRICAL] item {i}")

    def test_empirical_tiny_weight_frequency(self):
        tiny = Fraction(1, 1000)
        table = AliasTable([tiny, 1])
        p_tiny = float(table.normalized_weights[0])
        trials = 400_000
        source = SeededRandomSource(seed=999)
        counts = Counter(table.sample(source, trials))
        freq = counts.get(0, 0) / trials
        sigma = math.sqrt(p_tiny * (1 - p_tiny) / trials)
        self.assertAlmostEqual(freq, p_tiny, delta=5 * sigma,
                               msg="[EMPIRICAL] tiny weight")


if __name__ == "__main__":
    unittest.main(verbosity=2)
