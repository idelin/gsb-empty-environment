"""Exhaustive equivalence: interpreter vs optimized executor.

Domain {null, 0, 1}; all multisets (with duplicates, including the empty
table) up to a small size, for every supported subquery shape.  The
optimized executor is checked both with a large memory budget and with a
tiny budget (forcing partition spills) fed by single-row pages.
"""

import itertools
import os
import tempfile
import unittest

from minirel import (
    Col, CorrCond, Eq, ExistsPred, InPred, Lit, Or, Query, Subquery,
    run_query,
)

DOMAIN = (None, 0, 1)


def multisets(domain, max_size):
    out = [()]
    for n in range(1, max_size + 1):
        out.extend(itertools.combinations_with_replacement(domain, n))
    return [list(t) for t in out]


def pages_of(rows, size):
    return [rows[i:i + size] for i in range(0, len(rows), size)] or []


ONE_COL_PREDS = [
    # EXISTS, null-safe correlation
    ExistsPred(Subquery("i", corr=(CorrCond(Col(0), Col(0), True),))),
    # NOT EXISTS, regular correlation
    ExistsPred(Subquery("i", corr=(CorrCond(Col(0), Col(0), False),)),
               negated=True),
    # IN, uncorrelated
    InPred((Col(0),), Subquery("i", select=(Col(0),))),
    # NOT IN with inner filter (c0 = 0 OR c0 IS NULL)
    InPred((Col(0),),
           Subquery("i", select=(Col(0),),
                    filter=Or((Eq(Col(0), Lit(0)),
                               Eq(Col(0), Lit(None), True)))),
           negated=True),
    # NOT IN correlated null-safe
    InPred((Col(0),),
           Subquery("i", select=(Col(0),),
                    corr=(CorrCond(Col(0), Col(0), True),)),
           negated=True),
]

TWO_COL_PREDS = [
    # two-column row-value NOT IN
    InPred((Col(0), Col(1)),
           Subquery("i", select=(Col(0), Col(1))), negated=True),
    # two-column NOT IN with inner filter
    InPred((Col(0), Col(1)),
           Subquery("i", select=(Col(0), Col(1)),
                    filter=Eq(Col(1), Lit(1))),
           negated=True),
    # two-column IN, correlated on c0 (regular equality)
    InPred((Col(0), Col(1)),
           Subquery("i", select=(Col(0), Col(1)),
                    corr=(CorrCond(Col(0), Col(0)),))),
    # EXISTS with mixed null-safe / regular multi-column correlation
    ExistsPred(Subquery("i", corr=(CorrCond(Col(0), Col(1), True),
                                   CorrCond(Col(1), Col(0), False)))),
]


def query_variants(pred, ncols):
    cols = tuple(Col(i) for i in range(ncols))
    return [
        Query("o", select=cols, where=pred),          # WHERE variant
        Query("o", select=cols + (pred,)),            # projection variant
    ]


class EquivalenceBase:
    def check_all(self, preds, ncols, outer_tables, inner_tables, spill_dir):
        for pred in preds:
            for query in query_variants(pred, ncols):
                for outer in outer_tables:
                    tables = {"o": outer, "i": None}
                    for inner in inner_tables:
                        tables["i"] = inner
                        ref = run_query(query, tables, [outer],
                                        mode="interpreter")
                        opt = run_query(query, tables, [outer],
                                        mode="optimized")
                        low = run_query(
                            query, tables, pages_of(outer, 1),
                            mode="optimized", mem_budget=2,
                            spill_dir=spill_dir)
                        self.assertEqual(
                            ref.rows, opt.rows,
                            f"optimized mismatch: {query} o={outer} i={inner}")
                        self.assertEqual(
                            ref.rows, low.rows,
                            f"low-mem mismatch: {query} o={outer} i={inner}")


class TestOneColEquivalence(EquivalenceBase, unittest.TestCase):
    def test_exhaustive(self):
        # 20 multisets incl. empty & duplicates; rows are 1-tuples
        tables = [[(v,) for v in m] for m in multisets(DOMAIN, 3)]
        with tempfile.TemporaryDirectory() as tmp:
            spill_dir = os.path.join(tmp, "spill")
            self.check_all(ONE_COL_PREDS, 1, tables, tables, spill_dir)
            self.assertFalse(os.path.exists(spill_dir)
                             and os.listdir(spill_dir))


class TestTwoColEquivalence(EquivalenceBase, unittest.TestCase):
    def test_exhaustive(self):
        row_domain = list(itertools.product(DOMAIN, DOMAIN))  # 9 row values
        tables = multisets(row_domain, 2)  # 55 multisets
        with tempfile.TemporaryDirectory() as tmp:
            spill_dir = os.path.join(tmp, "spill")
            self.check_all(TWO_COL_PREDS, 2, tables, tables, spill_dir)
            self.assertFalse(os.path.exists(spill_dir)
                             and os.listdir(spill_dir))


if __name__ == "__main__":
    unittest.main()
