"""Unit tests for lowrank_update (stdlib unittest, no NumPy/SciPy)."""

import unittest
import warnings

from lowrank_update import (
    LUFactorization,
    SingularMatrixError,
    SingularUpdateError,
    UnstableUpdateWarning,
    dense_reference_solve,
    lu_factor,
    lu_solve,
    matvec,
    sherman_morrison_reverse_solve,
    sherman_morrison_solve,
    sherman_morrison_solve_many,
    sparse_vector,
    woodbury_solve,
)

TOL = 1e-9


def make_matrix(n, seed=12345):
    """Deterministic well-conditioned dense test matrix."""
    state = seed
    a = []
    for i in range(n):
        row = []
        for j in range(n):
            state = (1103515245 * state + 12345) % (2 ** 31)
            row.append((state / 2 ** 31) - 0.5)
        row[i] += n  # diagonal dominance keeps it well conditioned
        a.append(row)
    return a


def make_vector(n, seed=999):
    state = seed
    out = []
    for _ in range(n):
        state = (1103515245 * state + 12345) % (2 ** 31)
        out.append((state / 2 ** 31) - 0.5)
    return out


def rank1_updated(a, u, v):
    n = len(a)
    return [[a[i][j] + u[i] * v[j] for j in range(n)] for i in range(n)]


def assert_vec_close(tc, x, y, tol=TOL):
    tc.assertEqual(len(x), len(y))
    for xi, yi in zip(x, y):
        tc.assertAlmostEqual(xi, yi, delta=tol)


class TestLU(unittest.TestCase):
    def test_lu_solve_residual(self):
        for n in (1, 2, 5, 12):
            a = make_matrix(n, seed=n * 7 + 1)
            b = make_vector(n, seed=n * 13 + 2)
            lu, piv = lu_factor(a)
            x = lu_solve(lu, piv, b)
            assert_vec_close(self, matvec(a, x), b)

    def test_singular_matrix_rejected(self):
        with self.assertRaises(SingularMatrixError):
            lu_factor([[1.0, 2.0], [2.0, 4.0]])

    def test_non_square_rejected(self):
        with self.assertRaises(ValueError):
            lu_factor([[1.0, 2.0]])


class TestSparseVector(unittest.TestCase):
    def test_explicit_indices(self):
        v = sparse_vector(5, indices=[1, 3], values=[2.0, -1.0])
        self.assertEqual(v, [0.0, 2.0, 0.0, -1.0, 0.0])

    def test_duplicate_indices_accumulate(self):
        v = sparse_vector(3, indices=[0, 0], values=[1.0, 2.5])
        self.assertEqual(v, [3.5, 0.0, 0.0])

    def test_bad_index_rejected(self):
        with self.assertRaises(IndexError):
            sparse_vector(3, indices=[3], values=[1.0])
        with self.assertRaises(ValueError):
            sparse_vector(3, indices=[0], values=[1.0, 2.0])


class TestShermanMorrison(unittest.TestCase):
    def setUp(self):
        self.n = 8
        self.a = make_matrix(self.n, seed=42)
        self.fact = LUFactorization(self.a)
        self.u = make_vector(self.n, seed=7)
        self.v = make_vector(self.n, seed=11)
        self.b = make_vector(self.n, seed=23)

    def test_rank1_matches_dense_reference(self):
        x = sherman_morrison_solve(self.fact, self.u, self.v, self.b)
        ref = dense_reference_solve(rank1_updated(self.a, self.u, self.v), self.b)
        assert_vec_close(self, x, ref)

    def test_residual_of_updated_system(self):
        x = sherman_morrison_solve(self.fact, self.u, self.v, self.b)
        assert_vec_close(self, matvec(rank1_updated(self.a, self.u, self.v), x),
                         self.b)

    def test_reverse_update_recovers_solution(self):
        x_orig = self.fact.solve(self.b)
        x_rec = sherman_morrison_reverse_solve(self.fact, self.u, self.v, self.b)
        assert_vec_close(self, x_rec, x_orig)

    def test_update_then_reverse_round_trip(self):
        # Update solve, then reverse-update solve of the same RHS must
        # reproduce the pre-update solution.
        x_upd = sherman_morrison_solve(self.fact, self.u, self.v, self.b)
        x_back = sherman_morrison_reverse_solve(self.fact, self.u, self.v, self.b)
        self.assertGreater(max(abs(p - q) for p, q in zip(x_upd, x_back)), 1e-6)
        assert_vec_close(self, x_back, self.fact.solve(self.b))

    def test_singular_update_detected_and_factorization_survives(self):
        w = self.fact.solve(self.u)
        j = max(range(self.n), key=lambda i: abs(w[i]))
        v_bad = [0.0] * self.n
        v_bad[j] = -1.0 / w[j]  # 1 + v^T A^{-1} u == 0
        with self.assertRaises(SingularUpdateError):
            sherman_morrison_solve(self.fact, self.u, v_bad, self.b)
        # Original factorization must still solve the original system.
        assert_vec_close(self, matvec(self.a, self.fact.solve(self.b)), self.b)

    def test_near_zero_denominator_reports_instability(self):
        w = self.fact.solve(self.u)
        j = max(range(self.n), key=lambda i: abs(w[i]))
        v_bad = [0.0] * self.n
        v_bad[j] = -(1.0 - 1e-10) / w[j]  # denominator ~ 1e-10
        with warnings.catch_warnings(record=True) as caught:
            warnings.simplefilter("always")
            sherman_morrison_solve(self.fact, self.u, v_bad, self.b)
        self.assertTrue(any(issubclass(c.category, UnstableUpdateWarning)
                            for c in caught))

    def test_size_mismatch_rejected(self):
        with self.assertRaises(ValueError):
            sherman_morrison_solve(self.fact, self.u[:-1], self.v, self.b)
        with self.assertRaises(ValueError):
            sherman_morrison_solve(self.fact, self.u, self.v, self.b[:-1])


class TestMultipleRHS(unittest.TestCase):
    def test_multi_rhs_matches_single_and_reference(self):
        n = 6
        a = make_matrix(n, seed=5)
        fact = LUFactorization(a)
        u = make_vector(n, seed=17)
        v = make_vector(n, seed=19)
        bcols = [make_vector(n, seed=100 + i) for i in range(4)]
        updated = rank1_updated(a, u, v)

        xs_many = sherman_morrison_solve_many(fact, u, v, bcols)
        xs_single = [sherman_morrison_solve(fact, u, v, b) for b in bcols]
        refs = [dense_reference_solve(updated, b) for b in bcols]
        for xm, xs, xr in zip(xs_many, xs_single, refs):
            assert_vec_close(self, xm, xs)
            assert_vec_close(self, xm, xr)

    def test_lu_solve_many(self):
        n = 5
        a = make_matrix(n, seed=31)
        fact = LUFactorization(a)
        bcols = [make_vector(n, seed=200 + i) for i in range(3)]
        for x, b in zip(fact.solve_many(bcols), bcols):
            assert_vec_close(self, matvec(a, x), b)


class TestWoodbury(unittest.TestCase):
    def setUp(self):
        self.n = 7
        self.a = make_matrix(self.n, seed=77)
        self.fact = LUFactorization(self.a)
        self.b = make_vector(self.n, seed=88)

    def _updated(self, ucols, vcols):
        a2 = [row[:] for row in self.a]
        for uc, vc in zip(ucols, vcols):
            for i in range(self.n):
                for j in range(self.n):
                    a2[i][j] += uc[i] * vc[j]
        return a2

    def test_rank2_matches_dense_reference(self):
        ucols = [make_vector(self.n, seed=1), make_vector(self.n, seed=2)]
        vcols = [make_vector(self.n, seed=3), make_vector(self.n, seed=4)]
        x = woodbury_solve(self.fact, ucols, vcols, self.b)
        ref = dense_reference_solve(self._updated(ucols, vcols), self.b)
        assert_vec_close(self, x, ref)

    def test_rank3_residual(self):
        ucols = [make_vector(self.n, seed=s) for s in (5, 6, 7)]
        vcols = [make_vector(self.n, seed=s) for s in (8, 9, 10)]
        x = woodbury_solve(self.fact, ucols, vcols, self.b)
        assert_vec_close(self, matvec(self._updated(ucols, vcols), x), self.b)

    def test_rank1_woodbury_agrees_with_sherman_morrison(self):
        u = make_vector(self.n, seed=21)
        v = make_vector(self.n, seed=22)
        xw = woodbury_solve(self.fact, [u], [v], self.b)
        xs = sherman_morrison_solve(self.fact, u, v, self.b)
        assert_vec_close(self, xw, xs)

    def test_zero_rank_falls_back_to_plain_solve(self):
        assert_vec_close(self, woodbury_solve(self.fact, [], [], self.b),
                         self.fact.solve(self.b))

    def test_singular_capacitance_detected(self):
        # Duplicated rank-1 piece with v^T A^{-1} u = -1/2: the combined
        # update A + 2 u v^T is singular, so the 2-by-2 capacitance matrix
        # C = I + V^T A^{-1} U = [[1/2, -1/2], [-1/2, 1/2]] is singular.
        u = make_vector(self.n, seed=33)
        w = self.fact.solve(u)
        j = max(range(self.n), key=lambda i: abs(w[i]))
        v = [0.0] * self.n
        v[j] = -0.5 / w[j]
        with self.assertRaises(SingularUpdateError):
            woodbury_solve(self.fact, [u, u], [v, v], self.b)
        # Factorization still usable afterwards.
        assert_vec_close(self, matvec(self.a, self.fact.solve(self.b)), self.b)

    def test_column_count_mismatch_rejected(self):
        u = make_vector(self.n, seed=41)
        with self.assertRaises(ValueError):
            woodbury_solve(self.fact, [u], [], self.b)


if __name__ == "__main__":
    unittest.main(verbosity=2)
