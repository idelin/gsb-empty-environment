"""Equivalence and counterexample tests for null-rejection-based rewrites.

Synthetic tables are built locally; NULL is None; relations are bags.
No SQL engine is involved. Run: python3 -m unittest -v
"""

import unittest

from relalg import (
    And,
    Col,
    Cmp,
    IsNotNull,
    IsNull,
    Lit,
    Not,
    Or,
    Relation,
    TV,
    bag_equal,
    bag_key,
    inner_join,
    left_join,
    select,
)
from rewrites import (
    Filter,
    Join,
    PreconditionError,
    Scan,
    execute,
    is_null_rejecting,
    outer_to_inner,
    pushdown_after_conversion,
    pushdown_filter,
)

# ---------------------------------------------------------------------------
# Synthetic sample data (bags with duplicates and NULLs)
# ---------------------------------------------------------------------------
# L(lk is the join key): k=2 appears twice (duplicates), k=9 appears twice
# (duplicated UNMATCHED rows), k=NULL never matches anything.
L = Relation(
    "L",
    ("id", "lk"),
    [
        {"id": 1, "lk": 1},
        {"id": 2, "lk": 2},
        {"id": 3, "lk": 2},   # duplicate join key
        {"id": 4, "lk": 9},   # unmatched
        {"id": 5, "lk": 9},   # unmatched duplicate
        {"id": 6, "lk": None},  # NULL key: matches nothing
    ],
)
# R(rk is the join key): k=2 has three matches (multiplicity 3), one of
# them with x=NULL; k=3 matches nothing in L.
R = Relation(
    "R",
    ("rk", "x"),
    [
        {"rk": 1, "x": 5},
        {"rk": 2, "x": 1},
        {"rk": 2, "x": 4},
        {"rk": 2, "x": None},  # NULL value on the null-supplying side
        {"rk": 3, "x": 7},
    ],
)

JOIN_PRED = Cmp("=", Col("lk"), Col("rk"))
X_GT_3 = Cmp(">", Col("x"), Lit(3))       # null-rejecting on R
X_IS_NULL = IsNull(Col("x"))              # NOT null-rejecting on R
ID_LT_5 = Cmp("<", Col("id"), Lit(5))     # left-side-only predicate


def left_join_plan(pred):
    return Filter(pred, Join("left", Scan(L), Scan(R), JOIN_PRED))


def inner_join_plan(pred):
    return Filter(pred, Join("inner", Scan(L), Scan(R), JOIN_PRED))


class TestThreeValuedLogic(unittest.TestCase):
    def test_null_comparison_is_unknown(self):
        self.assertIs(Cmp(">", Col("x"), Lit(3)).eval({"x": None}), TV.UNKNOWN)
        self.assertIs(Cmp("=", Col("x"), Col("x")).eval({"x": None}), TV.UNKNOWN)

    def test_where_rejects_unknown_and_false(self):
        rel = Relation("T", ("x",), [{"x": 2}, {"x": 5}, {"x": None}])
        out = select(rel, X_GT_3)
        self.assertEqual(out.rows, [{"x": 5}])  # NULL row rejected, not kept

    def test_tv_algebra(self):
        self.assertIs(TV.TRUE & TV.UNKNOWN, TV.UNKNOWN)
        self.assertIs(TV.FALSE & TV.UNKNOWN, TV.FALSE)
        self.assertIs(TV.FALSE | TV.UNKNOWN, TV.UNKNOWN)
        self.assertIs(~TV.UNKNOWN, TV.UNKNOWN)


class TestNullRejectionAnalysis(unittest.TestCase):
    def test_comparison_is_null_rejecting(self):
        self.assertTrue(is_null_rejecting(X_GT_3, {"x"}))

    def test_is_null_is_not_null_rejecting(self):
        self.assertFalse(is_null_rejecting(X_IS_NULL, {"x"}))

    def test_is_not_null_is_null_rejecting(self):
        self.assertTrue(is_null_rejecting(IsNotNull(Col("x")), {"x"}))

    def test_or_with_is_null_is_not_null_rejecting(self):
        pred = Or(X_GT_3, X_IS_NULL)  # true on NULL -> not rejecting
        self.assertFalse(is_null_rejecting(pred, {"x"}))

    def test_and_with_null_rejecting_side(self):
        pred = And(X_GT_3, IsNull(Col("x")))
        self.assertTrue(is_null_rejecting(pred, {"x"}))

    def test_not_of_strict_comparison(self):
        self.assertTrue(is_null_rejecting(Not(X_GT_3), {"x"}))   # UNKNOWN
        self.assertFalse(is_null_rejecting(Not(X_IS_NULL), {"x"}))  # TRUE

    def test_unrelated_columns_not_rejecting(self):
        self.assertFalse(is_null_rejecting(X_GT_3, {"rk"}))


class TestPushdownInnerJoin(unittest.TestCase):
    """R1: sigma_p(A JOIN B) == sigma_p(A) JOIN B when p references A only."""

    def test_equivalence_and_multiplicity(self):
        original = inner_join_plan(ID_LT_5)
        rewritten = pushdown_filter(original)
        self.assertTrue(bag_equal(execute(original), execute(rewritten)))
        # multiplicity: id<5 keeps lk=2 twice, each meets R's three rk=2
        # rows -> 2*3 = 6 rows with lk=2 in the result.
        out = execute(rewritten)
        self.assertEqual(sum(1 for r in out.rows if r["lk"] == 2), 6)

    def test_right_side_pushdown_inner(self):
        original = inner_join_plan(X_GT_3)
        rewritten = pushdown_filter(original)
        self.assertTrue(bag_equal(execute(original), execute(rewritten)))

    def test_cross_side_predicate_refused(self):
        plan = inner_join_plan(Cmp("<", Col("lk"), Col("x")))
        with self.assertRaises(PreconditionError):
            pushdown_filter(plan)


class TestPushdownOuterJoin(unittest.TestCase):
    """R2: pushing onto the preserved side of a LEFT JOIN is valid;
    pushing onto the null-supplying side is refused (and wrong)."""

    def test_preserved_side_pushdown_equivalent(self):
        original = left_join_plan(ID_LT_5)
        rewritten = pushdown_filter(original)
        self.assertTrue(bag_equal(execute(original), execute(rewritten)))

    def test_null_supplying_side_pushdown_refused(self):
        with self.assertRaises(PreconditionError):
            pushdown_filter(left_join_plan(X_GT_3))
        with self.assertRaises(PreconditionError):
            pushdown_filter(left_join_plan(X_IS_NULL))

    def test_counterexample_is_null_pushdown_is_wrong(self):
        """Naively pushing 'x IS NULL' below the LEFT JOIN changes results:
        filtering R first turns previously-matched L rows into unmatched
        NULL-padded rows, inflating the result."""
        correct = select(left_join(L, R, JOIN_PRED), X_IS_NULL)
        naive = left_join(L, select(R, X_IS_NULL), JOIN_PRED)
        self.assertFalse(bag_equal(correct, naive))
        # correct: unmatched L rows (4,5,6) + matched row with x=NULL (2,3
        # match the rk=2,x=NULL row too) -> 5 rows; naive produces more
        # because rows with x=1/x=4 no longer match.
        self.assertEqual(len(correct.rows), 5)
        self.assertGreater(len(naive.rows), len(correct.rows))


class TestOuterToInnerConversion(unittest.TestCase):
    """R3: sigma_p(A LEFT JOIN B) == sigma_p(A JOIN B) iff p is
    null-rejecting on B (e.g. x > 3)."""

    def test_x_gt_3_conversion_equivalent(self):
        original = left_join_plan(X_GT_3)
        converted = outer_to_inner(original)
        self.assertEqual(converted.child.kind, "inner")
        self.assertTrue(bag_equal(execute(original), execute(converted)))

    def test_is_null_conversion_refused(self):
        with self.assertRaises(PreconditionError):
            outer_to_inner(left_join_plan(X_IS_NULL))

    def test_counterexample_is_null_conversion_is_wrong(self):
        """sigma_{x IS NULL}(L LEFTJOIN R) keeps NULL-padded unmatched rows;
        sigma_{x IS NULL}(L JOIN R) cannot produce them."""
        outer = select(left_join(L, R, JOIN_PRED), X_IS_NULL)
        inner = select(inner_join(L, R, JOIN_PRED), X_IS_NULL)
        self.assertFalse(bag_equal(outer, inner))
        self.assertEqual(len(outer.rows), 5)   # 3 unmatched + 2 matched x=NULL
        self.assertEqual(len(inner.rows), 2)   # only real x=NULL matches

    def test_pushdown_after_conversion_equivalent(self):
        """R4: sigma_{x>3}(L LEFTJOIN R) == L JOIN sigma_{x>3}(R)."""
        original = left_join_plan(X_GT_3)
        rewritten = pushdown_after_conversion(original)
        self.assertTrue(bag_equal(execute(original), execute(rewritten)))
        # lk=1 meets x=5 (1 row); lk=2 (twice) meets only x=4 (2 rows).
        self.assertEqual(len(execute(rewritten).rows), 3)


class TestMultiplicityPreservation(unittest.TestCase):
    def test_inner_join_multiplicity_is_product(self):
        out = inner_join(L, R, JOIN_PRED)
        # lk=1: 1*1, lk=2: 2*3, others 0 -> 7 rows.
        self.assertEqual(len(out.rows), 7)
        self.assertEqual(sum(1 for r in out.rows if r["lk"] == 2), 6)

    def test_left_join_preserves_unmatched_duplicates(self):
        out = left_join(L, R, JOIN_PRED)
        # 7 matched rows + 3 unmatched (id 4,5,6) = 10 rows.
        self.assertEqual(len(out.rows), 10)
        padded = [r for r in out.rows if r["rk"] is None]
        self.assertEqual(len(padded), 3)
        self.assertTrue(all(r["x"] is None for r in padded))

    def test_rewrite_preserves_bag_exactly(self):
        original = left_join_plan(ID_LT_5)
        rewritten = pushdown_filter(original)
        self.assertEqual(bag_key(execute(original)), bag_key(execute(rewritten)))

    def test_null_key_never_matches(self):
        out = inner_join(L, R, JOIN_PRED)
        self.assertFalse(any(r["lk"] is None for r in out.rows))


if __name__ == "__main__":
    unittest.main()
