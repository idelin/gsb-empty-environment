"""unittest suite for sptrisolve (Python 3.11, standard library only)."""

import random
import unittest

from sptrisolve import (DimensionError, MissingPivotError, NotTriangularError,
                        StructureError, TriangularMatrix, ZeroPivotError)
from sptrisolve import dense


def random_triangular_coo(rng, n, density, lower, unit_diagonal,
                          duplicate_rate=0.3):
    """Random triangular COO with shuffled storage order and duplicates."""
    rows, cols, values = [], [], []
    for i in range(n):
        for j in range(n):
            offdiag = (j < i) if lower else (i < j)
            if offdiag and rng.random() < density:
                rows.append(i)
                cols.append(j)
                values.append(round(rng.uniform(-4.0, 4.0), 6))
    if not unit_diagonal:
        for i in range(n):
            rows.append(i)
            cols.append(i)
            values.append(rng.choice([-1.0, 1.0]) * rng.uniform(0.5, 3.0))
    # Inject duplicates: split some entries into two additive contributions.
    count = len(rows)
    for k in range(count):
        if rng.random() < duplicate_rate:
            part = round(rng.uniform(-2.0, 2.0), 6)
            rows.append(rows[k])
            cols.append(cols[k])
            values.append(part)
            values[k] -= part
    # Permute the storage order so it carries no topological information.
    order = list(range(len(rows)))
    rng.shuffle(order)
    return ([rows[k] for k in order], [cols[k] for k in order],
            [values[k] for k in order])


def assert_vectors_close(test, actual, expected, places=9):
    test.assertEqual(len(actual), len(expected))
    for got, want in zip(actual, expected):
        test.assertAlmostEqual(got, want, places=places)


class TestConstruction(unittest.TestCase):
    def test_duplicate_coordinates_are_merged(self):
        rows = [2, 2, 1, 0, 1, 2]
        cols = [0, 0, 0, 0, 1, 2]
        vals = [1.5, 2.5, -1.0, 2.0, 3.0, 4.0]
        m = TriangularMatrix.from_coo(3, rows, cols, vals, lower=True)
        a = dense.dense_from_coo(3, rows, cols, vals)
        x = m.solve([6.0, 5.0, 4.0])
        assert_vectors_close(self, x, dense.dense_solve_lower(a, [6, 5, 4]))
        self.assertEqual(m._off[(2, 0)], 4.0)

    def test_duplicates_cancelling_to_zero_drop_offdiagonal(self):
        rows = [1, 1, 0, 1, 2]
        cols = [0, 0, 0, 1, 2]
        vals = [3.0, -3.0, 1.0, 1.0, 1.0]
        m = TriangularMatrix.from_coo(3, rows, cols, vals, lower=True)
        self.assertNotIn((1, 0), m._off)

    def test_non_triangular_entry_rejected_not_ignored(self):
        with self.assertRaises(NotTriangularError):
            TriangularMatrix.from_coo(3, [0, 1, 0], [0, 1, 2], [1, 1, 9],
                                      lower=True)
        with self.assertRaises(NotTriangularError):
            TriangularMatrix.from_coo(3, [0, 1, 2], [0, 1, 0], [1, 1, 9],
                                      lower=False)
        m = TriangularMatrix.from_coo(3, [0, 1, 2], [0, 1, 2], [1, 1, 1],
                                      lower=True)
        with self.assertRaises(NotTriangularError):
            m.insert(0, 2, 1.0)

    def test_out_of_range_and_bad_index_rejected(self):
        with self.assertRaises(StructureError):
            TriangularMatrix.from_coo(2, [0, 2], [0, 1], [1, 1], lower=True)
        with self.assertRaises(StructureError):
            TriangularMatrix.from_coo(2, [0, -1], [0, 0], [1, 1], lower=True)
        with self.assertRaises(DimensionError):
            TriangularMatrix.from_coo(2, [0, 1.5], [0, 1], [1, 1], lower=True)
        with self.assertRaises(DimensionError):
            TriangularMatrix(3.5, lower=True)

    def test_ragged_coo_rejected(self):
        with self.assertRaises(DimensionError):
            TriangularMatrix.from_coo(2, [0, 1], [0], [1, 1], lower=True)

    def test_unit_diagonal_rejects_stored_diagonal(self):
        with self.assertRaises(StructureError):
            TriangularMatrix.from_coo(2, [0, 1], [0, 1], [1, 1], lower=True,
                                      unit_diagonal=True)

    def test_missing_and_zero_pivot_are_distinct(self):
        # Missing pivot: structural error at construction time.
        with self.assertRaises(MissingPivotError):
            TriangularMatrix.from_coo(3, [0, 2, 1], [0, 2, 0], [1, 1, 5],
                                      lower=True)
        # Zero pivot: numerical error at solve time, different type.
        m = TriangularMatrix.from_coo(3, [0, 1, 2], [0, 1, 2], [1, 0, 1],
                                      lower=True)
        with self.assertRaises(ZeroPivotError):
            m.solve([1.0, 2.0, 3.0])
        self.assertNotEqual(MissingPivotError, ZeroPivotError)
        self.assertFalse(issubclass(ZeroPivotError, MissingPivotError))


class TestSolveCorrectness(unittest.TestCase):
    def check_random(self, n, lower, unit_diagonal, seed):
        rng = random.Random(seed)
        rows, cols, vals = random_triangular_coo(rng, n, 0.4, lower,
                                                 unit_diagonal)
        m = TriangularMatrix.from_coo(n, rows, cols, vals, lower=lower,
                                      unit_diagonal=unit_diagonal)
        a = dense.dense_from_coo(n, rows, cols, vals)
        b = [rng.uniform(-10, 10) for _ in range(n)]
        x = m.solve(b)
        if lower:
            ref = dense.dense_solve_lower(a, b, unit_diagonal)
        else:
            ref = dense.dense_solve_upper(a, b, unit_diagonal)
        assert_vectors_close(self, x, ref)
        # Residual check against the dense assembly.
        if unit_diagonal:
            for i in range(n):
                a[i][i] = 1.0
        assert_vectors_close(self, dense.matvec(a, x), b)

    def test_lower_random_permuted_storage(self):
        for seed in range(8):
            self.check_random(n=25, lower=True, unit_diagonal=False,
                              seed=seed)

    def test_upper_random_permuted_storage(self):
        for seed in range(8):
            self.check_random(n=25, lower=False, unit_diagonal=False,
                              seed=100 + seed)

    def test_unit_diagonal_random(self):
        for seed in range(8):
            self.check_random(n=20, lower=True, unit_diagonal=True,
                              seed=200 + seed)

    def test_transpose_solve_matches_dense(self):
        rng = random.Random(7)
        n = 18
        rows, cols, vals = random_triangular_coo(rng, n, 0.5, lower=True,
                                                 unit_diagonal=False)
        m = TriangularMatrix.from_coo(n, rows, cols, vals, lower=True)
        a_t = dense.transpose(dense.dense_from_coo(n, rows, cols, vals))
        b = [rng.uniform(-5, 5) for _ in range(n)]
        # Lower transpose is solved as an upper-triangular system.
        assert_vectors_close(self, m.T.solve(b),
                             dense.dense_solve_upper(a_t, b))
        assert_vectors_close(self, m.solve(b, transpose=True),
                             dense.dense_solve_upper(a_t, b))

    def test_rhs_length_checked(self):
        m = TriangularMatrix.from_coo(2, [0, 1], [0, 1], [1, 1], lower=True)
        with self.assertRaises(DimensionError):
            m.solve([1.0])
        with self.assertRaises(DimensionError):
            m.solve(3.0)

    def test_zero_pivot_publishes_no_partial_solution(self):
        # Pivot of row 2 is zero; rows 0 and 1 would already be solved when
        # the failure is hit, but the caller must receive nothing.
        m = TriangularMatrix.from_coo(3, [0, 1, 2, 2], [0, 1, 2, 0],
                                      [1.0, 1.0, 0.0, 5.0], lower=True)
        with self.assertRaises(ZeroPivotError):
            m.solve([1.0, 2.0, 3.0])
        # After fixing the pivot numerically, the same matrix solves fine.
        m.set_diagonal(2, 2.0)
        x = m.solve([1.0, 2.0, 3.0])
        assert_vectors_close(self, x, [1.0, 2.0, -1.0])


class TestSchedulingAndAnalysis(unittest.TestCase):
    def chain_matrix(self, n=6):
        rows = list(range(n)) + [i for i in range(1, n)]
        cols = list(range(n)) + [i - 1 for i in range(1, n)]
        vals = [1.0] * n + [1.0] * (n - 1)
        return TriangularMatrix.from_coo(n, rows, cols, vals, lower=True)

    def test_levels_respect_dependencies(self):
        rng = random.Random(11)
        n = 30
        rows, cols, vals = random_triangular_coo(rng, n, 0.3, lower=True,
                                                 unit_diagonal=False)
        m = TriangularMatrix.from_coo(n, rows, cols, vals, lower=True)
        ana = m.analysis()
        level_of = {}
        for depth, level in enumerate(ana.levels):
            for node in level:
                level_of[node] = depth
        evidence = ana.dependency_evidence()
        for node, deps in evidence.items():
            for dep in deps:
                self.assertLess(level_of[dep], level_of[node])

    def test_storage_order_does_not_change_schedule(self):
        rng = random.Random(3)
        n = 20
        rows, cols, vals = random_triangular_coo(rng, n, 0.4, lower=True,
                                                 unit_diagonal=False,
                                                 duplicate_rate=0.0)
        m1 = TriangularMatrix.from_coo(n, rows, cols, vals, lower=True)
        order = list(range(len(rows)))
        rng.shuffle(order)
        m2 = TriangularMatrix.from_coo(n, [rows[k] for k in order],
                                       [cols[k] for k in order],
                                       [vals[k] for k in order], lower=True)
        self.assertEqual(m1.analysis().levels, m2.analysis().levels)
        b = [rng.uniform(-1, 1) for _ in range(n)]
        assert_vectors_close(self, m1.solve(b), m2.solve(b))

    def test_diagonal_matrix_is_fully_parallel(self):
        n = 9
        m = TriangularMatrix.from_coo(n, list(range(n)), list(range(n)),
                                      [2.0] * n, lower=True)
        ana = m.analysis()
        self.assertEqual(len(ana.levels), 1)
        self.assertEqual(ana.parallel_widths(), [n])
        self.assertEqual(ana.dependency_evidence(),
                         {i: [] for i in range(n)})

    def test_chain_matrix_is_fully_sequential(self):
        m = self.chain_matrix(6)
        ana = m.analysis()
        self.assertEqual(ana.parallel_widths(), [1] * 6)
        self.assertEqual(ana.dependency_evidence()[5], [4])

    def test_solve_many_shares_one_analysis(self):
        m = self.chain_matrix(5)
        rhs = [[float(i + k) for i in range(5)] for k in range(4)]
        xs = m.solve_many(rhs)
        for b, x in zip(rhs, xs):
            assert_vectors_close(self, x, m.solve(b))
        stats = m.stats()
        # One build for solve_many plus one per individual solve check.
        self.assertEqual(stats["analysis_builds"], 1)
        self.assertGreaterEqual(stats["analysis_reuses"], 4)
        self.assertGreater(stats["analysis_reuse_rate"], 0.5)

    def test_numeric_update_keeps_analysis(self):
        m = self.chain_matrix(4)
        m.solve([1.0, 1.0, 1.0, 1.0])
        before = m.stats()["analysis_builds"]
        m.set_value(2, 1, 3.0)
        m.set_diagonal(0, 2.0)
        x = m.solve([2.0, 1.0, 1.0, 1.0])
        self.assertEqual(m.stats()["analysis_builds"], before)
        a = [[2.0, 0, 0, 0], [1.0, 1, 0, 0], [0, 3.0, 1, 0], [0, 0, 1.0, 1]]
        assert_vectors_close(self, x, dense.dense_solve_lower(a, [2, 1, 1, 1]))


class TestIncrementalInvalidation(unittest.TestCase):
    def test_insert_reanalyses_only_affected_nodes(self):
        n = 10
        m = TriangularMatrix.from_coo(n, list(range(n)), list(range(n)),
                                      [1.0] * n, lower=True)
        m.analysis()
        m.analysis(transpose=True)
        stats_before = m.stats()
        m.insert(7, 3, 2.0)  # only node 7 depends on node 3 afterwards
        stats = m.stats()
        self.assertEqual(stats["structure_version"], 1)
        self.assertEqual(stats["nodes_recomputed"], 2)  # one node per view
        ana = m.analysis()
        self.assertEqual(ana.last_recomputed, [7])
        self.assertEqual(ana.dependency_evidence()[7], [3])
        # Level of unaffected nodes untouched; result still correct.
        b = [1.0] * n
        x = m.solve(b)
        a = [[0.0] * n for _ in range(n)]
        for i in range(n):
            a[i][i] = 1.0
        a[7][3] = 2.0
        assert_vectors_close(self, x, dense.dense_solve_lower(a, b))

    def test_remove_updates_schedule_and_result(self):
        rows = [0, 1, 2, 2]
        cols = [0, 1, 2, 0]
        vals = [1.0, 1.0, 1.0, 4.0]
        m = TriangularMatrix.from_coo(3, rows, cols, vals, lower=True)
        m.solve([1.0, 1.0, 1.0])
        m.remove(2, 0)
        ana = m.analysis()
        self.assertEqual(ana.dependency_evidence()[2], [])
        self.assertEqual(ana.parallel_widths(), [3])
        assert_vectors_close(self, m.solve([1.0, 1.0, 1.0]), [1.0, 1.0, 1.0])

    def test_transpose_view_tracks_structure_changes(self):
        m = TriangularMatrix.from_coo(3, [0, 1, 2], [0, 1, 2], [1, 1, 1],
                                      lower=True)
        m.T.solve([1.0, 1.0, 1.0])
        m.insert(2, 0, 1.0)
        ana_t = m.T.analysis()
        self.assertEqual(ana_t.dependency_evidence()[0], [2])
        a_t = [[1.0, 0, 1.0], [0, 1.0, 0], [0, 0, 1.0]]
        assert_vectors_close(self, m.T.solve([3.0, 1.0, 1.0]),
                             dense.dense_solve_upper(a_t, [3, 1, 1]))

    def test_transpose_view_properties(self):
        m = TriangularMatrix.from_coo(2, [0, 1], [0, 1], [1, 1], lower=True)
        self.assertFalse(m.T.lower)
        self.assertIs(m.T, m.T)
        self.assertEqual(m.T.n, 2)


if __name__ == "__main__":
    unittest.main()
