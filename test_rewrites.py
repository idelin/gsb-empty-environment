"""Equivalence and counterexample tests for the rewrite rules.

Every valid rule is tested with its precondition satisfied (including
duplicate rows, to pin down bag multiplicities); every way to violate a
precondition is backed by an explicit counterexample showing the results
actually differ.
"""

from __future__ import annotations

import unittest

from ra.expr import And, Cmp, Col, IsNotNull, IsNull, Lit, Not, Or, is_null_rejecting
from ra.rel import bag_eq, inner_join, left_join, select
from ra.rewrite import (
    R1_PUSHDOWN_INNER,
    R2_PUSHDOWN_LEFTJOIN_PRESERVED,
    R3_OUTER_TO_INNER,
    X1_PUSHDOWN_NULL_SUPPLYING,
    r1_original,
    r2_original,
    r3_original,
    x1_original,
)
from ra.tvl import NULL

R = [
    {"a": 1, "b": "x"},
    {"a": 1, "b": "x"},   # duplicate
    {"a": 2, "b": "y"},
    {"a": 3, "b": "z"},   # unmatched by the join
    {"a": 4, "b": NULL},
]
S = [
    {"c": 1, "x": 5},
    {"c": 1, "x": 5},     # duplicate
    {"c": 1, "x": 2},
    {"c": 2, "x": 7},
    {"c": 2, "x": NULL},
]
R_ATTRS = frozenset({"a", "b"})
S_ATTRS = frozenset({"c", "x"})
THETA = Cmp("=", Col("a"), Col("c"))

X_GT_3 = Cmp(">", Col("x"), Lit(3))
X_IS_NULL = IsNull(Col("x"))
B_EQ_X = Cmp("=", Col("b"), Lit("x"))


class TestNullRejection(unittest.TestCase):
    def test_comparison_rejects_null(self):
        self.assertTrue(is_null_rejecting(X_GT_3, S_ATTRS))

    def test_is_null_accepts_null(self):
        self.assertFalse(is_null_rejecting(X_IS_NULL, S_ATTRS))

    def test_is_not_null_rejects_null(self):
        self.assertTrue(is_null_rejecting(IsNotNull(Col("x")), S_ATTRS))

    def test_not_of_unknown_producing_pred_rejects_null(self):
        # NOT (x > 3): NULL -> UNKNOWN -> UNKNOWN, still non-TRUE.
        self.assertTrue(is_null_rejecting(Not(X_GT_3), S_ATTRS))

    def test_not_of_is_null_rejects_null(self):
        # NOT (x IS NULL) == x IS NOT NULL.
        self.assertTrue(is_null_rejecting(Not(X_IS_NULL), S_ATTRS))

    def test_and_needs_one_null_rejecting_side(self):
        pred = And(X_GT_3, Cmp("<", Col("c"), Lit(10)))
        self.assertTrue(is_null_rejecting(pred, frozenset({"x"})))
        self.assertTrue(is_null_rejecting(pred, frozenset({"c"})))

    def test_or_needs_both_sides_null_rejecting(self):
        pred = Or(X_GT_3, Cmp(">", Col("c"), Lit(3)))
        self.assertFalse(is_null_rejecting(pred, frozenset({"x"})))
        self.assertTrue(is_null_rejecting(pred, S_ATTRS))

    def test_or_with_is_null_disjunct_accepts_null(self):
        pred = Or(X_GT_3, X_IS_NULL)
        self.assertFalse(is_null_rejecting(pred, S_ATTRS))

    def test_comparison_against_null_literal_rejects_null(self):
        self.assertTrue(is_null_rejecting(Cmp("=", Col("b"), Lit(NULL)), R_ATTRS))


class TestR1PushdownInnerJoin(unittest.TestCase):
    def test_precondition_accepts_right_side_predicate(self):
        self.assertTrue(R1_PUSHDOWN_INNER.check(X_GT_3, R_ATTRS, S_ATTRS))

    def test_precondition_rejects_left_side_predicate(self):
        self.assertFalse(R1_PUSHDOWN_INNER.check(B_EQ_X, R_ATTRS, S_ATTRS))

    def test_equivalence_with_duplicates_and_nulls(self):
        before = r1_original(R, S, THETA, X_GT_3)
        after = R1_PUSHDOWN_INNER.apply(R, S, THETA, X_GT_3, S_ATTRS)
        self.assertTrue(bag_eq(before, after))

    def test_multiplicity_is_product_of_duplicate_counts(self):
        # a=1 twice in R x c=1,x=5 twice in S = 4 rows, plus one x=7 row.
        before = r1_original(R, S, THETA, X_GT_3)
        self.assertEqual(5, len(before))
        self.assertEqual(5, len(inner_join(R, select(S, X_GT_3), THETA)))


class TestR2PushdownPreservedSide(unittest.TestCase):
    def test_precondition_accepts_left_side_predicate(self):
        self.assertTrue(R2_PUSHDOWN_LEFTJOIN_PRESERVED.check(B_EQ_X, R_ATTRS, S_ATTRS))

    def test_precondition_rejects_right_side_predicate(self):
        self.assertFalse(R2_PUSHDOWN_LEFTJOIN_PRESERVED.check(X_GT_3, R_ATTRS, S_ATTRS))

    def test_equivalence_with_duplicates_and_nulls(self):
        before = r2_original(R, S, THETA, B_EQ_X, S_ATTRS)
        after = R2_PUSHDOWN_LEFTJOIN_PRESERVED.apply(R, S, THETA, B_EQ_X, S_ATTRS)
        self.assertTrue(bag_eq(before, after))
        # Two duplicate {'a':1,'b':'x'} rows each match three S rows: 2*3 = 6.
        self.assertEqual(6, len(after))


class TestR3OuterToInner(unittest.TestCase):
    def test_precondition_holds_for_null_rejecting_predicate(self):
        self.assertTrue(R3_OUTER_TO_INNER.check(X_GT_3, R_ATTRS, S_ATTRS))

    def test_precondition_fails_for_is_null(self):
        self.assertFalse(R3_OUTER_TO_INNER.check(X_IS_NULL, R_ATTRS, S_ATTRS))

    def test_equivalence_when_null_rejecting(self):
        before = r3_original(R, S, THETA, X_GT_3, S_ATTRS)
        after = R3_OUTER_TO_INNER.apply(R, S, THETA, X_GT_3, S_ATTRS)
        self.assertTrue(bag_eq(before, after))

    def test_counterexample_is_null_predicate(self):
        # sigma_{x IS NULL}(R ⟕ S) keeps padded rows; the inner join drops them.
        before = r3_original(R, S, THETA, X_IS_NULL, S_ATTRS)
        after = R3_OUTER_TO_INNER.apply(R, S, THETA, X_IS_NULL, S_ATTRS)
        self.assertFalse(bag_eq(before, after))
        self.assertEqual(3, len(before))   # stored x=NULL row + a=3, a=4 padded
        self.assertEqual(1, len(after))    # only the stored x=NULL row matches


class TestX1RejectedPushdownNullSupplyingSide(unittest.TestCase):
    def test_rule_is_never_applicable(self):
        self.assertFalse(X1_PUSHDOWN_NULL_SUPPLYING.check(X_GT_3, R_ATTRS, S_ATTRS))
        self.assertFalse(X1_PUSHDOWN_NULL_SUPPLYING.check(X_IS_NULL, R_ATTRS, S_ATTRS))

    def test_counterexample_is_null(self):
        # Filtering S to x-IS-NULL rows first fabricates NULL-padded output
        # for left rows that originally matched (and were then filtered out).
        before = x1_original(R, S, THETA, X_IS_NULL, S_ATTRS)
        after = X1_PUSHDOWN_NULL_SUPPLYING.apply(R, S, THETA, X_IS_NULL, S_ATTRS)
        self.assertFalse(bag_eq(before, after))

    def test_counterexample_null_rejecting_predicate(self):
        # Even x > 3 cannot be pushed: unmatched left rows survive as padded
        # rows in the rewritten plan but are filtered out in the original.
        before = x1_original(R, S, THETA, X_GT_3, S_ATTRS)
        after = X1_PUSHDOWN_NULL_SUPPLYING.apply(R, S, THETA, X_GT_3, S_ATTRS)
        self.assertFalse(bag_eq(before, after))
        self.assertEqual(5, len(before))
        self.assertEqual(7, len(after))    # 5 matched + a=3, a=4 padded

    def test_counterexample_minimal_is_null(self):
        # Smallest witness: one matching pair, predicate x IS NULL.
        r = [{"a": 1, "b": "x"}]
        s = [{"c": 1, "x": 5}]
        before = x1_original(r, s, THETA, X_IS_NULL, S_ATTRS)
        after = X1_PUSHDOWN_NULL_SUPPLYING.apply(r, s, THETA, X_IS_NULL, S_ATTRS)
        self.assertEqual([], before)
        self.assertEqual([{"a": 1, "b": "x", "c": NULL, "x": NULL}], after)


class TestBagSemantics(unittest.TestCase):
    def test_left_join_pads_each_unmatched_occurrence_once(self):
        r = [{"a": 9, "b": "u"}, {"a": 9, "b": "u"}]
        out = left_join(r, S, THETA, S_ATTRS)
        self.assertEqual(2, len(out))
        self.assertTrue(all(row["x"] is NULL and row["c"] is NULL for row in out))

    def test_inner_join_multiplicity_multiplies(self):
        r = [{"a": 1, "b": "x"}, {"a": 1, "b": "x"}]
        s = [{"c": 1, "x": 5}, {"c": 1, "x": 5}, {"c": 1, "x": 5}]
        self.assertEqual(6, len(inner_join(r, s, THETA)))

    def test_select_drops_unknown_not_just_false(self):
        rows = [{"x": 5}, {"x": NULL}, {"x": 1}]
        self.assertEqual([{"x": 5}], select(rows, X_GT_3))


if __name__ == "__main__":
    unittest.main()
