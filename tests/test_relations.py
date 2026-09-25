import random
import unittest

from joinplanner.relations import (INNER, LEFT_OUTER, Relation, exact_ndv,
                                   execute_join)


class RelationSemanticsTest(unittest.TestCase):
    def test_inner_join_multiset_multiplicity(self):
        a = Relation("A", ("k", "x"), [(1, "a1"), (1, "a2")])
        b = Relation("B", ("k", "y"), [(1, "b1"), (1, "b2"), (1, "b3")])
        r = execute_join(a, b, "k", "k", INNER)
        self.assertEqual(r.nrows, 2 * 3)  # bag product
        self.assertEqual(r.multiset(), {
            (1, "a1", 1, "b1"): 1,
            (1, "a1", 1, "b2"): 1,
            (1, "a1", 1, "b3"): 1,
            (1, "a2", 1, "b1"): 1,
            (1, "a2", 1, "b2"): 1,
            (1, "a2", 1, "b3"): 1,
        })

    def test_null_never_matches_inner(self):
        a = Relation("A", ("k",), [(None,), (1,), (2,)])
        b = Relation("B", ("k",), [(None,), (1,)])
        r = execute_join(a, b, "k", "k", INNER)
        self.assertEqual(r.rows, [(1, 1)])  # NULL=NULL and NULL=1 both false

    def test_left_outer_null_padding(self):
        a = Relation("A", ("k", "x"), [(1, "a1"), (2, "a2"), (None, "a3")])
        b = Relation("B", ("k", "y"), [(1, "b1")])
        r = execute_join(a, b, "k", "k", LEFT_OUTER)
        self.assertEqual(r.nrows, 3)
        self.assertIn((2, "a2", None, None), r.rows)
        self.assertIn((None, "a3", None, None), r.rows)
        self.assertIn((1, "a1", 1, "b1"), r.rows)

    def test_partitioned_equals_inmemory_inner(self):
        rng = random.Random(42)
        vals = [None, 1, 2, 3, 4]
        a = Relation("A", ("k", "p"), [(rng.choice(vals), i) for i in range(800)])
        b = Relation("B", ("k", "q"), [(rng.choice(vals), i) for i in range(800)])
        mem = execute_join(a, b, "k", "k", INNER, partitioned=False)
        part = execute_join(a, b, "k", "k", INNER, partitioned=True, partitions=7)
        self.assertEqual(mem.nrows, part.nrows)
        self.assertEqual(mem.multiset(), part.multiset())

    def test_partitioned_equals_inmemory_left_outer(self):
        rng = random.Random(123)
        vals = [None, 10, 20, 30]
        a = Relation("A", ("k", "p"), [(rng.choice(vals), i) for i in range(500)])
        b = Relation("B", ("k", "q"), [(rng.choice(vals), i) for i in range(200)])
        mem = execute_join(a, b, "k", "k", LEFT_OUTER, partitioned=False)
        part = execute_join(a, b, "k", "k", LEFT_OUTER, partitioned=True, partitions=5)
        self.assertEqual(mem.multiset(), part.multiset())

    def test_column_renaming_and_lineage(self):
        a = Relation("A", ("k", "v"), [(1, 10)])
        b = Relation("B", ("k", "w"), [(1, 20)])
        r = execute_join(a, b, "k", "k", INNER)
        self.assertEqual(r.columns, ("k", "v", "B.k", "w"))
        self.assertIn(("A", "k"), r.lineage)
        self.assertIn(("B", "k"), r.lineage)

    def test_exact_ndv_ignores_null(self):
        r = Relation("R", ("k",), [(1,), (1,), (2,), (None,), (None,)])
        self.assertEqual(exact_ndv(r, "k"), 2)


if __name__ == "__main__":
    unittest.main()
