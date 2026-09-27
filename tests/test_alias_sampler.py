import json
import math
import random
import unittest
from fractions import Fraction

from alias_sampler import (
    AliasTable,
    AliasTablePublisher,
    WeightError,
    empirical_frequencies,
    enumerate_exact_distribution,
)


class TestWeightValidation(unittest.TestCase):
    def test_negative_weight_rejected(self):
        for bad in ([1, -1, 2], [Fraction(-1, 3)], [-0.5, 1.0]):
            with self.assertRaises(WeightError):
                AliasTable(bad)

    def test_non_finite_weight_rejected(self):
        for bad in ([1.0, math.inf], [math.nan, 1.0], [-math.inf]):
            with self.assertRaises(WeightError):
                AliasTable(bad)

    def test_empty_and_all_zero_rejected(self):
        with self.assertRaises(WeightError):
            AliasTable([])
        with self.assertRaises(WeightError):
            AliasTable([0, 0, 0])

    def test_bool_and_str_rejected(self):
        with self.assertRaises(WeightError):
            AliasTable([True, 1])
        with self.assertRaises(WeightError):
            AliasTable(["1", 2])


class TestExactConstruction(unittest.TestCase):
    def test_theoretical_probabilities_equal_normalized_weights(self):
        # [EXACT] table reconstruction == normalized original weights.
        cases = [
            [1, 2, 3, 4],
            [Fraction(1, 3), Fraction(1, 7), Fraction(5, 11)],
            [0, 5, 0, 1, 9],
            [Fraction(1, 10**18), 1, 2],
            [Fraction(1, 10**30), Fraction(10**30)],
            [7],
            [1] * 50,
        ]
        for weights in cases:
            table = AliasTable(weights)
            self.assertEqual(table.probabilities(), table.normalized_weights())
            self.assertEqual(sum(table.probabilities()), Fraction(1))

    def test_no_normalization_error(self):
        # [EXACT] probabilities sum to exactly 1 even for awkward weights.
        weights = [Fraction(1, p) for p in (3, 7, 11, 13, 17, 19)]
        table = AliasTable(weights)
        self.assertEqual(sum(table.probabilities()), Fraction(1))

    def test_single_positive_weight_always_selected(self):
        table = AliasTable([0, 0, 42, 0])
        rng = random.Random(1234)
        for _ in range(1000):
            self.assertEqual(table.sample(rng), 2)
        self.assertEqual(table.probabilities()[2], Fraction(1))

    def test_zero_weight_never_selected(self):
        table = AliasTable([0, 1, 0, 3, 0])
        rng = random.Random(99)
        for _ in range(5000):
            self.assertIn(table.sample(rng), (1, 3))
        probs = table.probabilities()
        self.assertEqual(probs[0], Fraction(0))
        self.assertEqual(probs[2], Fraction(0))
        self.assertEqual(probs[4], Fraction(0))

    def test_tiny_weight_exact_probability(self):
        tiny = Fraction(1, 10**15)
        table = AliasTable([tiny, 1])
        # [EXACT] the tiny item keeps its exact probability, no underflow.
        self.assertEqual(table.probabilities()[0], tiny / (tiny + 1))


class TestExactEnumeration(unittest.TestCase):
    def test_enumeration_proves_exact_distribution(self):
        # [EXACT] enumerate every (column, threshold) outcome for small sets.
        cases = [
            [1, 2, 3],
            [0, 1, 1, 2],
            [Fraction(1, 2), Fraction(1, 3), Fraction(1, 6)],
            [5],
            [0, 7],
            [3, 1, 4, 1, 5, 9, 2, 6],
        ]
        for weights in cases:
            table = AliasTable(weights)
            enumerated = enumerate_exact_distribution(table)
            self.assertEqual(enumerated, table.normalized_weights())
            self.assertEqual(sum(enumerated), Fraction(1))

    def test_enumeration_outcome_space_guard(self):
        table = AliasTable([Fraction(1, 10**9), 1])
        with self.assertRaises(ValueError):
            enumerate_exact_distribution(table, max_outcomes=1000)


class TestSerializationReplay(unittest.TestCase):
    def test_round_trip_preserves_table_exactly(self):
        weights = [Fraction(1, 7), 3, 0, Fraction(5, 2), Fraction(1, 10**12)]
        table = AliasTable(weights)
        restored = AliasTable.loads(table.dumps())
        self.assertEqual(restored.weights, table.weights)
        self.assertEqual(restored.prob, table.prob)
        self.assertEqual(restored.alias, table.alias)
        self.assertEqual(restored.probabilities(), table.probabilities())

    def test_deserialized_table_introduces_no_bias(self):
        # [EXACT] probabilities reconstructed after reload match exactly.
        table = AliasTable([2, 5, 11, 0, Fraction(1, 13)])
        restored = AliasTable.loads(table.dumps())
        self.assertEqual(restored.probabilities(), table.normalized_weights())

    def test_serialized_format_is_plain_json(self):
        data = json.loads(AliasTable([1, 2]).dumps())
        self.assertEqual(data["format"], "exact-alias-table/1")
        self.assertEqual(data["prob"], ["2/3", "1/1"])
        self.assertEqual(data["alias"], [1, 1])

    def test_corrupt_table_rejected(self):
        data = AliasTable([1, 2]).to_dict()
        data["prob"] = ["3/2", "1/2"]
        with self.assertRaises(ValueError):
            AliasTable.from_dict(data)
        data = AliasTable([1, 2]).to_dict()
        data["alias"] = [0, 7]
        with self.assertRaises(ValueError):
            AliasTable.from_dict(data)

    def test_session_replay_reproduces_sequence(self):
        publisher = AliasTablePublisher([1, 2, 3, 0, 5])
        session = publisher.open_session(seed=20260927)
        produced = session.sample(500)
        self.assertEqual(session.draws, 500)
        self.assertEqual(session.replay(), produced)

    def test_replay_after_serialization(self):
        publisher = AliasTablePublisher([Fraction(1, 9), 4, 0, 2])
        session = publisher.open_session(seed="replay-me")
        produced = session.sample(300)
        restored = AliasTable.loads(session.table.dumps())
        rng = random.Random(session.seed)
        replayed = [restored.sample(rng) for _ in range(session.draws)]
        self.assertEqual(replayed, produced)


class TestDualVersionPublishing(unittest.TestCase):
    def test_old_session_keeps_old_distribution(self):
        publisher = AliasTablePublisher([1, 1, 1])
        old_session = publisher.open_session(seed=7)
        old_table = old_session.table

        publisher.publish_update([100, 1, 0])
        self.assertEqual(publisher.version, 1)

        self.assertIs(old_session.table, old_table)
        self.assertEqual(old_session.version, 0)
        self.assertEqual(old_session.table.probabilities(), [Fraction(1, 3)] * 3)

        new_session = publisher.open_session(seed=7)
        self.assertEqual(new_session.version, 1)
        self.assertEqual(
            new_session.table.probabilities(),
            [Fraction(100, 101), Fraction(1, 101), Fraction(0)],
        )

    def test_batch_update_publishes_single_new_version(self):
        publisher = AliasTablePublisher([1, 2])
        publisher.publish_update([3, 4, 5])
        publisher.publish_update([9])
        self.assertEqual(publisher.version, 2)
        self.assertEqual(publisher.current_table().probabilities(), [Fraction(1)])

    def test_seed_and_draws_recorded(self):
        publisher = AliasTablePublisher([1, 1])
        session = publisher.open_session(seed=42)
        session.sample(17)
        state = session.state_dict()
        self.assertEqual(state, {"version": 0, "seed": 42, "draws": 17})


class TestEmpiricalAgreement(unittest.TestCase):
    def test_empirical_frequencies_track_exact_probabilities(self):
        # [EMPIRICAL] fixed seed -> deterministic, loose tolerance.
        weights = [1, 2, 3, 4]
        table = AliasTable(weights)
        rng = random.Random(2026)
        draws = 200_000
        freqs, counts = empirical_frequencies(table, rng, draws)
        self.assertEqual(sum(counts), draws)
        exact = table.normalized_weights()
        for observed, expected in zip(freqs, exact):
            self.assertAlmostEqual(observed, float(expected), delta=0.01)


if __name__ == "__main__":
    unittest.main()
