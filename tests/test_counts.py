"""Execution counts and plan explanation."""

import unittest

from minirel import (
    Col, CorrCond, ExistsPred, InPred, Query, Subquery, run_query,
)


class TestCounts(unittest.TestCase):
    def setUp(self):
        self.outer = [(v,) for v in [None, 0, 1] * 10]          # 30 rows
        self.inner = [(v,) for v in [None, 0, 1, 2] * 10]        # 40 rows
        self.tables = {"o": self.outer, "i": self.inner}

    def test_scan_counts(self):
        query = Query("o", select=(Col(0),),
                      where=InPred((Col(0),),
                                   Subquery("i", select=(Col(0),))))
        interp = run_query(query, self.tables, [self.outer],
                           mode="interpreter")
        opt = run_query(query, self.tables, [self.outer], mode="optimized")
        # interpreter: full inner scan per outer row
        self.assertEqual(interp.stats.inner_rows_scanned, 30 * 40)
        # optimized: each inner row indexed exactly once, one probe per
        # outer row (single in-memory partition)
        self.assertEqual(opt.stats.inner_rows_indexed, 40)
        self.assertEqual(opt.stats.index_lookups, 30)
        self.assertEqual(opt.stats.partitions_spilled, 0)
        self.assertEqual(interp.rows, opt.rows)

    def test_exists_short_circuit_counts(self):
        query = Query("o", select=(Col(0),),
                      where=ExistsPred(Subquery("i")))
        interp = run_query(query, self.tables, [self.outer],
                           mode="interpreter")
        # EXISTS stops at the first matching inner row
        self.assertLessEqual(interp.stats.inner_rows_scanned, 30 * 40)

    def test_plan_explanation(self):
        query = Query(
            "o",
            select=(Col(0),),
            where=InPred((Col(0), Col(1)),
                         Subquery("i", select=(Col(0), Col(1)),
                                  corr=(CorrCond(Col(0), Col(0), True),)),
                         negated=True),
        )
        opt = run_query(query, {"o": [], "i": []}, [], mode="optimized")
        self.assertIn("MarkerJoin[NOT IN]", opt.plan)
        self.assertIn("<=>", opt.plan)  # null-safe correlation shown
        q2 = Query("o", select=(Col(0),),
                   where=ExistsPred(Subquery("i"), negated=True))
        opt2 = run_query(q2, {"o": [], "i": []}, [], mode="optimized")
        self.assertIn("AntiJoin[NOT EXISTS]", opt2.plan)
        q3 = Query("o", select=(Col(0),), where=ExistsPred(Subquery("i")))
        opt3 = run_query(q3, {"o": [], "i": []}, [], mode="optimized")
        self.assertIn("SemiJoin[EXISTS]", opt3.plan)


if __name__ == "__main__":
    unittest.main()
