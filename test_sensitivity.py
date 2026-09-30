"""Unit tests for the sensitivity analyzer, evaluator and finite-domain verifier.

Run with:  python3.11 -m unittest test_sensitivity -v
"""

import unittest

from sensitivity import (
    Aggregate, Clip, Compose, GroupBy, Join, Table, TableDomain,
    analyze, enumerate_sensitivity, evaluate, verify,
)


class TestStaticBounds(unittest.TestCase):
    def test_sum_bound_is_m_times_b(self):
        q = Aggregate(Table("T", 3, 2.0), "sum")
        analysis = analyze(q)
        self.assertTrue(analysis.bounded)
        self.assertEqual(analysis.bound, 6.0)

    def test_count_bound_is_m(self):
        q = Aggregate(Table("T", 3, 2.0), "count")
        self.assertEqual(analyze(q).bound, 3)

    def test_missing_row_cap_is_unbounded(self):
        q = Aggregate(Table("T", None, 2.0), "sum")
        analysis = analyze(q)
        self.assertFalse(analysis.bounded)
        self.assertIsNone(analysis.bound)
        self.assertIn("UNBOUNDED", analysis.derivation.render())

    def test_missing_value_cap_unbounded_for_sum_but_not_count(self):
        t = Table("T", 3, None)
        self.assertIsNone(analyze(Aggregate(t, "sum")).bound)
        self.assertEqual(analyze(Aggregate(t, "count")).bound, 3)

    def test_clip_shrinks_value_bound(self):
        q = Aggregate(Clip(Table("T", 3, 2.0), hi=1.0), "sum")
        self.assertEqual(analyze(q).bound, 3.0)

    def test_clip_wider_than_b_is_noop(self):
        q = Aggregate(Clip(Table("T", 3, 1.0), hi=5.0), "sum")
        self.assertEqual(analyze(q).bound, 3.0)

    def test_groupby_sum_not_multiplied_by_groups(self):
        q = GroupBy(Table("T", 3, 2.0), groups=("g1", "g2", "g3"), agg="sum")
        self.assertEqual(analyze(q).bound, 6.0)

    def test_groupby_count_bound_is_m(self):
        q = GroupBy(Table("T", 3, 2.0), groups=("g1", "g2"), agg="count")
        self.assertEqual(analyze(q).bound, 3)

    def test_join_multiplies_row_bounds(self):
        q = Aggregate(Join(Table("L", 2, 3.0), Table("R", 3, 5.0)), "count")
        self.assertEqual(analyze(q).bound, 6)

    def test_join_sum_uses_carried_side_value(self):
        q = Aggregate(Join(Table("L", 2, 3.0), Table("R", 3, 5.0),
                           value_side="left"), "sum")
        self.assertEqual(analyze(q).bound, 2 * 3 * 3.0)
        q_r = Aggregate(Join(Table("L", 2, 3.0), Table("R", 3, 5.0),
                             value_side="right"), "sum")
        self.assertEqual(analyze(q_r).bound, 2 * 3 * 5.0)

    def test_join_with_unbounded_side_rejected(self):
        q = Aggregate(Join(Table("L", 2, 3.0), Table("R", None, 5.0)), "count")
        self.assertIsNone(analyze(q).bound)

    def test_self_join_is_quadratic_not_operator_local_max(self):
        # Correlated contributions: a subject with m rows on each side of a
        # self-join produces m*m joined rows, not m and not 2*m.
        q = Aggregate(Join(Table("T", 2, 1.0), Table("T", 2, 1.0)), "count")
        self.assertEqual(analyze(q).bound, 4)

    def test_compose_adds_sensitivities(self):
        t = Table("T", 3, 2.0)
        q = Compose((Aggregate(t, "sum"), Aggregate(t, "count")))
        self.assertEqual(analyze(q).bound, 6.0 + 3)

    def test_compose_with_unbounded_part_rejected(self):
        q = Compose((Aggregate(Table("T", 3, 2.0), "sum"),
                     Aggregate(Table("U", None, 2.0), "count")))
        self.assertIsNone(analyze(q).bound)

    def test_derivation_tree_records_every_step(self):
        q = GroupBy(Clip(Join(Table("L", 2, 3.0), Table("R", 2, 5.0)), hi=2.0),
                    groups=("g",), agg="sum")
        tree = analyze(q).derivation.render()
        for step in ("group-by sum", "clip", "join", "table L", "table R"):
            self.assertIn(step, tree)


class TestEvaluator(unittest.TestCase):
    def test_evaluate_sum_count_groupby(self):
        t = Table("T", 10, 10.0)
        data = {"T": [("s1", "g1", 1.0), ("s1", "g2", 2.0), ("s2", "g1", 4.0)]}
        self.assertEqual(evaluate(Aggregate(t, "sum"), data), [7.0])
        self.assertEqual(evaluate(Aggregate(t, "count"), data), [3.0])
        self.assertEqual(evaluate(GroupBy(t, ("g1", "g2"), "sum"), data),
                         [5.0, 2.0])

    def test_evaluate_clip(self):
        t = Table("T", 10, 10.0)
        data = {"T": [("s1", "g", 4.0), ("s2", "g", 1.0)]}
        self.assertEqual(evaluate(Aggregate(Clip(t, 2.0), "sum"), data), [3.0])

    def test_evaluate_join_on_subject(self):
        q = Aggregate(Join(Table("L", 10, 10.0), Table("R", 10, 10.0)), "count")
        data = {"L": [("s1", "g", 1.0), ("s1", "g", 2.0), ("s2", "g", 3.0)],
                "R": [("s1", "g", 5.0), ("s1", "g", 6.0)]}
        # s1: 2*2 = 4 pairs; s2 has no right rows; cross-subject pairs none.
        self.assertEqual(evaluate(q, data), [4.0])

    def test_evaluate_compose_concatenates(self):
        t = Table("T", 10, 10.0)
        data = {"T": [("s1", "g", 2.0)]}
        q = Compose((Aggregate(t, "sum"), Aggregate(t, "count")))
        self.assertEqual(evaluate(q, data), [2.0, 1.0])


class TestFiniteDomainVerification(unittest.TestCase):
    """Enumerate adjacent synthetic datasets: the derived upper bound must
    never be violated, and the witness must attain it (lower bound == upper
    bound) for these queries."""

    def check_tight(self, query, domains):
        analysis, witness, ok, tight = verify(query, domains)
        self.assertTrue(ok, f"bound violated: {witness.max_l1} > {analysis.bound}")
        self.assertTrue(tight, f"bound {analysis.bound} not attained, "
                               f"observed {witness.max_l1}")
        return analysis, witness

    def test_sum_tight(self):
        t = Table("T", 3, 2.0)
        analysis, witness = self.check_tight(
            Aggregate(t, "sum"),
            {"T": TableDomain(("g",), (0.0, 2.0), 3)})
        self.assertEqual(witness.max_l1, 6.0)

    def test_count_tight(self):
        t = Table("T", 3, 2.0)
        self.check_tight(Aggregate(t, "count"),
                         {"T": TableDomain(("g",), (0.0, 2.0), 3)})

    def test_clip_tight(self):
        t = Table("T", 3, 2.0)
        _, witness = self.check_tight(
            Aggregate(Clip(t, 1.0), "sum"),
            {"T": TableDomain(("g",), (0.0, 2.0), 3)})
        self.assertEqual(witness.max_l1, 3.0)

    def test_groupby_sum_tight_with_rows_spread_across_groups(self):
        t = Table("T", 2, 2.0)
        _, witness = self.check_tight(
            GroupBy(t, ("g1", "g2"), "sum"),
            {"T": TableDomain(("g1", "g2"), (0.0, 2.0), 2)})
        self.assertEqual(witness.max_l1, 4.0)

    def test_groupby_count_tight(self):
        t = Table("T", 2, 2.0)
        self.check_tight(GroupBy(t, ("g1", "g2"), "count"),
                         {"T": TableDomain(("g1", "g2"), (0.0, 2.0), 2)})

    def test_self_join_count_tight_quadratic(self):
        t = Table("T", 2, 1.0)
        _, witness = self.check_tight(
            Aggregate(Join(t, t), "count"),
            {"T": TableDomain(("g",), (0.0, 1.0), 2)})
        # One subject with 2 rows on each side contributes 4 joined rows:
        # an operator-local max (2) would be violated here.
        self.assertEqual(witness.max_l1, 4.0)

    def test_join_sum_tight(self):
        q = Aggregate(Join(Table("L", 2, 3.0), Table("R", 2, 5.0),
                           value_side="left"), "sum")
        _, witness = self.check_tight(
            q, {"L": TableDomain(("g",), (0.0, 3.0), 2),
                "R": TableDomain(("g",), (0.0, 5.0), 2)})
        self.assertEqual(witness.max_l1, 12.0)

    def test_compose_tight_single_subject(self):
        t = Table("T", 2, 2.0)
        _, witness = self.check_tight(
            Compose((Aggregate(t, "sum"), Aggregate(t, "count"))),
            {"T": TableDomain(("g",), (0.0, 2.0), 2)})
        # m*b + m = 6 attained by ONE subject: contributions are correlated.
        self.assertEqual(witness.max_l1, 6.0)
        for rows in witness.added_rows.values():
            subjects = {s for (s, _, _) in rows}
            self.assertEqual(subjects, {witness.subject})

    def test_witness_is_adjacent_pair(self):
        t = Table("T", 2, 2.0)
        witness = enumerate_sensitivity(
            Aggregate(t, "sum"),
            {"T": TableDomain(("g",), (0.0, 2.0), 2)})
        # The differing subject has at most m rows (adjacency = add/remove
        # one subject) and the L1 distance matches the reported outputs.
        n_rows = sum(len(rows) for rows in witness.added_rows.values())
        self.assertLessEqual(n_rows, 2)
        l1 = sum(abs(a - b) for a, b in
                 zip(witness.output_with, witness.output_without))
        self.assertAlmostEqual(l1, witness.max_l1)


if __name__ == "__main__":
    unittest.main()
