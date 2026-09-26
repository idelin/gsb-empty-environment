"""Targeted semantics: two-column NOT IN, empty correlated sets, unknown
preservation in projection, WHERE filtering."""

import unittest

from minirel import (
    Col, CorrCond, Eq, ExistsPred, InPred, Lit, Query, Subquery, run_query,
)


def run_both(query, tables, outer):
    interp = run_query(query, tables, [outer], mode="interpreter")
    opt = run_query(query, tables, [outer], mode="optimized")
    return interp, opt


def notin2_query():
    return Query(
        "o",
        select=(Col(0), Col(1),
                InPred((Col(0), Col(1)),
                       Subquery("i", select=(Col(0), Col(1))), negated=True)),
        where=None,
    )


class TestTwoColumnNotIn(unittest.TestCase):
    def check(self, inner, outer_row, expected_marker):
        tables = {"o": [outer_row], "i": inner}
        interp, opt = run_both(notin2_query(), tables, tables["o"])
        self.assertEqual(interp.rows, opt.rows)
        self.assertEqual(len(opt.rows), 1)
        self.assertIs(opt.rows[0][2], expected_marker)

    def test_null_elsewhere_does_not_force_unknown(self):
        # Inner has a null, but (1,2) vs (3,null) is definitely FALSE,
        # so NOT IN is TRUE.
        self.check([(3, None)], (1, 2), True)

    def test_unknown(self):
        # (1,2) vs (1,null) is UNKNOWN -> NOT IN UNKNOWN.
        self.check([(1, None)], (1, 2), None)

    def test_false(self):
        # An exact match exists -> NOT IN FALSE, the other null row is
        # irrelevant.
        self.check([(1, 2), (9, None)], (1, 2), False)

    def test_empty_inner(self):
        self.check([], (1, 2), True)


class TestEmptyCorrelatedSet(unittest.TestCase):
    """No inner row correlates with the outer row."""

    def setUp(self):
        self.tables = {"o": [(1,)], "i": [(7,), (None,)]}
        self.corr = (CorrCond(Col(0), Col(0)),)  # regular equality

    def probe(self, pred):
        query = Query("o", select=(Col(0), pred))
        interp, opt = run_both(query, self.tables, self.tables["o"])
        self.assertEqual(interp.rows, opt.rows)
        return opt.rows[0][1]

    def test_exists_false(self):
        self.assertIs(self.probe(ExistsPred(Subquery("i", corr=self.corr))),
                      False)

    def test_not_exists_true(self):
        pred = ExistsPred(Subquery("i", corr=self.corr), negated=True)
        self.assertIs(self.probe(pred), True)

    def test_in_false(self):
        pred = InPred((Col(0),), Subquery("i", select=(Col(0),),
                                          corr=self.corr))
        self.assertIs(self.probe(pred), False)

    def test_not_in_true(self):
        pred = InPred((Col(0),), Subquery("i", select=(Col(0),),
                                          corr=self.corr), negated=True)
        self.assertIs(self.probe(pred), True)


class TestProjectionAndWhere(unittest.TestCase):
    def test_projection_preserves_unknown(self):
        pred = InPred((Col(0),), Subquery("i", select=(Col(0),)))
        query = Query("o", select=(Col(0), pred))
        tables = {"o": [(None,), (1,)], "i": [(0,)]}
        interp, opt = run_both(query, tables, tables["o"])
        # null IN {0} -> UNKNOWN stays None; 1 IN {0} -> FALSE.
        self.assertEqual(opt.rows, [(None, None), (1, False)])

    def test_where_keeps_only_true(self):
        pred = InPred((Col(0),), Subquery("i", select=(Col(0),)))
        query = Query("o", select=(Col(0),), where=pred)
        tables = {"o": [(None,), (0,), (1,)], "i": [(0,), (None,)]}
        interp, opt = run_both(query, tables, tables["o"])
        # None -> unknown (dropped), 0 -> TRUE, 1 -> unknown (dropped).
        self.assertEqual(opt.rows, [(0,)])

    def test_outer_multiplicity_preserved(self):
        pred = ExistsPred(Subquery("i", corr=(CorrCond(Col(0), Col(0)),)))
        query = Query("o", select=(Col(0),), where=pred)
        tables = {"o": [(1,), (1,), (1,), (2,)], "i": [(1,)]}
        interp, opt = run_both(query, tables, tables["o"])
        self.assertEqual(opt.rows, [(1,), (1,), (1,)])

    def test_inner_filter_applies(self):
        pred = InPred((Col(0),),
                      Subquery("i", select=(Col(0),),
                               filter=Eq(Col(1), Lit(5))))
        query = Query("o", select=(Col(0), pred))
        tables = {"o": [(1,)], "i": [(1, 4), (1, 5)]}
        interp, opt = run_both(query, tables, tables["o"])
        self.assertEqual(opt.rows, [(1, True)])


if __name__ == "__main__":
    unittest.main()
