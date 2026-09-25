import unittest
from itertools import product

from allen import (
    ALL_RELATIONS_MASK,
    COMPOSITION_TABLE,
    Duration,
    IntegerInterval,
    Interval,
    IntervalNetwork,
    RELATIONS,
    TimePoint,
    WallClock,
    exhaustive_integer_witness,
    find_integer_witness,
    format_relations,
    inverse_mask,
    relation_between_integer_intervals,
    relation_mask,
)


def build_hard_network() -> IntervalNetwork:
    """Four items occupy one of three pairwise meeting slots."""
    network = IntervalNetwork(7)
    slots = (0, 1, 2)
    items = (3, 4, 5, 6)
    network.restrict(0, 1, "m")
    network.restrict(1, 2, "m")

    # Rows are the three slot values. Columns are relations from each slot.
    signature = (
        ("=", "mi", "<"),
        ("m", "=", "mi"),
        (">", "m", "="),
    )
    for item in items:
        for slot_index, slot in enumerate(slots):
            network.restrict(slot, item, signature[slot_index])
    for left_index, left in enumerate(items):
        for right in items[left_index + 1:]:
            network.restrict(
                left, right,
                [relation for relation in RELATIONS if relation != "="]
            )
    return network


def assert_witness_satisfies_network(test_case, network, witness):
    size = network.size
    for i in range(size):
        for j in range(size):
            allowed = network.relations(i, j)
            actual = relation_between_integer_intervals(witness[i], witness[j])
            test_case.assertIn(actual, allowed)


class AllenAlgebraTests(unittest.TestCase):
    def test_before_transitivity(self):
        network = IntervalNetwork.from_constraints(
            3,
            ((0, 1, "<"), (1, 2, "<")),
        )

        result = network.propagate()

        self.assertTrue(result.is_consistent)
        self.assertEqual(network.relations(0, 2), ("<",))
        self.assertEqual(network.relations(2, 0), (">",))

    def test_synthetic_composition_table_matches_all_endpoint_orders(self):
        from allen import _relation_between_integer

        index = {symbol: i for i, symbol in enumerate(RELATIONS)}
        expected = {
            (first, second): set()
            for first in RELATIONS
            for second in RELATIONS
        }
        for a0, a1, b0, b1, c0, c1 in product(range(6), repeat=6):
            if not (a0 < a1 and b0 < b1 and c0 < c1):
                continue
            first = _relation_between_integer((a0, a1), (b0, b1))
            second = _relation_between_integer((b0, b1), (c0, c1))
            composed = _relation_between_integer((a0, a1), (c0, c1))
            expected[(first, second)].add(composed)

        for first in RELATIONS:
            for second in RELATIONS:
                self.assertEqual(
                    set(COMPOSITION_TABLE[index[first]][index[second]]),
                    expected[(first, second)],
                    msg=f"{first} o {second}",
                )

    def test_synthetic_composition_table_has_core_entries(self):
        index = {symbol: i for i, symbol in enumerate(RELATIONS)}
        table = COMPOSITION_TABLE

        self.assertEqual(set(table[index["<"]][index["<"]]), {"<"})
        self.assertEqual(set(table[index[">"]][index[">"]]), {">"})
        self.assertIn("=", table[index["m"]][index["mi"]])
        self.assertIn("=", table[index["d"]][index["di"]])
        for i, symbol in enumerate(RELATIONS):
            self.assertEqual(inverse_mask(1 << i).bit_count(), 1)

    def test_before_chain_has_integer_witness(self):
        network = IntervalNetwork.from_constraints(
            3,
            ((0, 1, "<"), (1, 2, "<")),
        )
        network.propagate()
        result = find_integer_witness(network)

        self.assertTrue(result.is_satisfiable)
        self.assertEqual(result.witness, {
            0: IntegerInterval(0, 1),
            1: IntegerInterval(2, 3),
            2: IntegerInterval(4, 5),
        })
        assert_witness_satisfies_network(self, network, result.witness)

    def test_local_path_consistency_is_not_global_satisfiability(self):
        network = build_hard_network()
        propagation = network.propagate()

        self.assertTrue(
            propagation.is_consistent,
            msg=propagation.conflict.explanation()
            if propagation.conflict else "",
        )
        for i in range(network.size):
            self.assertNotEqual(network.mask(i, i), 0)

        independent_witness = exhaustive_integer_witness(
            network, max_endpoint=6
        )
        search_result = find_integer_witness(network)

        print("\nHard network path-consistency status:", propagation.status)
        print("Independent integer endpoint enumeration (0..6):",
              independent_witness)
        print("Complete atomic search status:", search_result.status)
        print("Branches explored:", search_result.branches_explored)
        for failed in search_result.failed_branches:
            print("Conflict branch:", failed.assignments)
            print("  ", failed.conflict.explanation())

        self.assertIsNone(independent_witness)
        self.assertTrue(search_result.is_unsatisfiable)
        self.assertGreaterEqual(search_result.branches_explored, 2)
        self.assertTrue(search_result.failed_branches)
        for failed in search_result.failed_branches:
            self.assertIsNotNone(failed.conflict)
            self.assertTrue(failed.conflict.explanation())

    def test_satisfiable_search_returns_verifiable_witness(self):
        network = IntervalNetwork.from_constraints(
            3,
            (
                (0, 1, "<m"),
                (1, 2, "<m"),
                (0, 2, "<"),
            ),
        )

        result = find_integer_witness(network)

        self.assertTrue(result.is_satisfiable)
        self.assertIsNotNone(result.witness)
        assert_witness_satisfies_network(self, network, result.witness)

    def test_push_pop_restores_domains_queue_and_evidence(self):
        network = IntervalNetwork(4)
        network.restrict(0, 1, "<m")
        network.restrict(1, 2, "<m")
        network.propagate()
        self.assertEqual(network.relations(0, 2), ("<",))
        self.assertIsNotNone(network.evidence(0, 2))

        saved_domain = network.mask(2, 3)
        saved_processed = network.processed_arcs
        network.push()
        network.restrict(2, 3, "<")
        partial = network.propagate(budget=1)
        self.assertTrue(partial.budget_exhausted)
        self.assertTrue(network.pending_arcs)
        self.assertIsNotNone(network.evidence(2, 3))

        network.pop()

        self.assertEqual(network.mask(2, 3), saved_domain)
        self.assertEqual(network.mask(3, 2), ALL_RELATIONS_MASK)
        self.assertEqual(network.pending_arcs, ())
        self.assertIsNone(network.evidence(2, 3))
        self.assertEqual(network.relations(0, 2), ("<",))
        self.assertIsNotNone(network.evidence(0, 2))
        self.assertEqual(network.processed_arcs, saved_processed)

        result = network.propagate()
        self.assertTrue(result.is_consistent)

    def test_budget_exhaustion_is_unknown_not_unsatisfiable(self):
        network = IntervalNetwork(5)
        for i in range(4):
            network.restrict(i, i + 1, ("<", "m", "oi"))
        result = network.propagate(budget=0)

        self.assertTrue(result.budget_exhausted)
        self.assertFalse(result.is_consistent)
        self.assertIsNone(result.conflict)
        self.assertTrue(network.pending_arcs)

        search_result = find_integer_witness(
            network, propagation_budget=0
        )
        self.assertTrue(search_result.is_unknown)
        self.assertIsNone(search_result.witness)
        self.assertIsNone(search_result.conflict)

    def test_rollback_conflicting_correction_recovers_search(self):
        network = IntervalNetwork(3)
        network.restrict(0, 1, "<")
        network.push()
        direct_conflict = network.restrict(0, 1, ">")
        self.assertIsNotNone(direct_conflict)
        network.pop()

        self.assertEqual(network.relations(0, 1), ("<",))
        self.assertEqual(network.relations(1, 0), (">",))
        result = network.propagate()
        self.assertTrue(result.is_consistent)
        witness_result = find_integer_witness(network)
        self.assertTrue(witness_result.is_satisfiable)
        assert_witness_satisfies_network(
            self, network, witness_result.witness
        )

    def test_time_point_duration_and_wall_clock_are_distinct(self):
        start = TimePoint(10)
        later = start + Duration.seconds(2)
        self.assertEqual(later - start, Duration.seconds(2))
        interval = Interval(start, later)
        self.assertEqual(interval.duration, Duration(2_000_000_000))

        wall = WallClock.utc(2026, 9, 26, 12, 0, 0)
        point = wall.to_time_point()
        self.assertIsInstance(point, TimePoint)
        self.assertNotIsInstance(wall, (TimePoint, Duration))
        with self.assertRaises(TypeError):
            start + wall  # type: ignore[operator]


if __name__ == "__main__":
    unittest.main(verbosity=2)
