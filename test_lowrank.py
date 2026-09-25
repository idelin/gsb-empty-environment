"""unittest suite for lowrank.py (pure stdlib, Python 3.11)."""

import random
import unittest

from lowrank import (
    LU,
    LowRankUpdate,
    RankOneUpdate,
    SingularMatrixError,
    SingularUpdateError,
    dense_from_sparse,
    matvec,
)


def random_matrix(n, rng, diag_boost=1.0):
    """Well-conditioned random dense matrix (diagonally dominant)."""
    return [[rng.uniform(-1.0, 1.0) + (diag_boost * n if i == j else 0.0)
             for j in range(n)] for i in range(n)]


def max_err(x, y):
    return max(abs(a - b) for a, b in zip(x, y))


def residual(a, x, b):
    return max(abs(ai - bi) for ai, bi in zip(matvec(a, x), b))


class TestDenseLU(unittest.TestCase):
    def setUp(self):
        self.rng = random.Random(42)

    def test_solve_matches_residual(self):
        n = 6
        a = random_matrix(n, self.rng)
        lu = LU(a, n)
        b = [self.rng.uniform(-5, 5) for _ in range(n)]
        x = lu.solve(b)
        self.assertLess(residual(a, x, b), 1e-10)

    def test_singular_matrix_detected(self):
        a = [[1.0, 2.0], [2.0, 4.0]]  # rank deficient
        with self.assertRaises(SingularMatrixError):
            LU(a, 2)

    def test_wrong_size_rejected(self):
        with self.assertRaises(ValueError):
            LU([[1.0, 2.0]], 2)

    def test_multiple_rhs(self):
        n = 5
        a = random_matrix(n, self.rng)
        lu = LU(a, n)
        rhs = [[self.rng.uniform(-1, 1) for _ in range(n)] for _ in range(4)]
        for x, b in zip(lu.solve_many(rhs), rhs):
            self.assertLess(residual(a, x, b), 1e-10)


class TestRankOneUpdate(unittest.TestCase):
    def setUp(self):
        self.rng = random.Random(7)
        self.n = 8
        self.a = random_matrix(self.n, self.rng)
        self.lu = LU(self.a, self.n)
        # sparse u, v via explicit indices
        self.u_idx = [0, 3, 5]
        self.u_val = [1.5, -2.0, 0.75]
        self.v_idx = [1, 4, 7]
        self.v_val = [0.5, 1.25, -1.0]
        self.u = dense_from_sparse(self.n, self.u_idx, self.u_val)
        self.v = dense_from_sparse(self.n, self.v_idx, self.v_val)
        self.a_upd = [[self.a[i][j] + self.u[i] * self.v[j]
                       for j in range(self.n)] for i in range(self.n)]

    def test_matches_direct_dense_solve(self):
        upd = RankOneUpdate(self.lu, self.u_idx, self.u_val,
                            self.v_idx, self.v_val, self.n)
        b = [self.rng.uniform(-3, 3) for _ in range(self.n)]
        x_upd = upd.solve(b)
        x_ref = LU(self.a_upd, self.n).solve(b)  # direct reference
        self.assertLess(max_err(x_upd, x_ref), 1e-9)
        self.assertLess(residual(self.a_upd, x_upd, b), 1e-9)

    def test_multiple_rhs_consistent_with_reference(self):
        upd = RankOneUpdate(self.lu, self.u_idx, self.u_val,
                            self.v_idx, self.v_val, self.n)
        ref = LU(self.a_upd, self.n)
        rhs = [[self.rng.uniform(-2, 2) for _ in range(self.n)]
               for _ in range(5)]
        for x_upd, x_ref in zip(upd.solve_many(rhs), ref.solve_many(rhs)):
            self.assertLess(max_err(x_upd, x_ref), 1e-9)

    def test_base_factorization_not_refactorized(self):
        # The update must reuse, not replace, the base LU internals.
        lu_snapshot = (self.lu._lu, self.lu._perm)
        RankOneUpdate(self.lu, self.u_idx, self.u_val,
                      self.v_idx, self.v_val, self.n)
        self.assertIs(self.lu._lu, lu_snapshot[0])
        self.assertIs(self.lu._perm, lu_snapshot[1])

    def test_singular_update_detected_and_base_survives(self):
        # u = -A e_2, v = e_2  =>  A + u v^T has a zero column => singular,
        # and the Sherman-Morrison denominator 1 + v^T A^-1 u = 0.
        n = self.n
        col2 = [self.a[i][2] for i in range(n)]
        u_idx = list(range(n))
        u_val = [-c for c in col2]
        v_idx, v_val = [2], [1.0]
        with self.assertRaises(SingularUpdateError) as ctx:
            RankOneUpdate(self.lu, u_idx, u_val, v_idx, v_val, n)
        self.assertAlmostEqual(ctx.exception.denominator, 0.0, places=12)
        # original factorization still solves the original system
        b = [self.rng.uniform(-1, 1) for _ in range(n)]
        x = self.lu.solve(b)
        self.assertLess(residual(self.a, x, b), 1e-10)

    def test_near_zero_denominator_reported_unstable(self):
        # denominator = 1 + v^T A^-1 u = 1e-14 -> within tolerance of 0
        n = self.n
        w = self.lu.solve(dense_from_sparse(n, [0], [1.0]))  # A^-1 e_0
        # choose v = alpha * e_0 with alpha such that 1 + alpha*w[0] = 1e-14
        alpha = (1e-14 - 1.0) / w[0]
        with self.assertRaises(SingularUpdateError):
            RankOneUpdate(self.lu, [0], [1.0], [0], [alpha], n)

    def test_reverse_update_recovers_solution(self):
        upd = RankOneUpdate(self.lu, self.u_idx, self.u_val,
                            self.v_idx, self.v_val, self.n)
        back = upd.reverse_update()  # (A + u v^T) - u v^T = A
        b = [self.rng.uniform(-3, 3) for _ in range(self.n)]
        x_recovered = back.solve(b)
        x_original = self.lu.solve(b)
        self.assertLess(max_err(x_recovered, x_original), 1e-8)
        self.assertLess(residual(self.a, x_recovered, b), 1e-8)

    def test_dimension_mismatch_rejected(self):
        with self.assertRaises(ValueError):
            RankOneUpdate(self.lu, [0], [1.0], [0], [1.0], self.n + 1)
        with self.assertRaises(IndexError):
            RankOneUpdate(self.lu, [self.n], [1.0], [0], [1.0], self.n)


class TestLowRankUpdate(unittest.TestCase):
    def setUp(self):
        self.rng = random.Random(11)
        self.n = 10
        self.k = 3
        self.a = random_matrix(self.n, self.rng)
        self.lu = LU(self.a, self.n)
        # k sparse columns for U and V with explicit indices
        self.u_cols = [([0, 2], [1.0, 0.5]),
                       ([4], [-1.5]),
                       ([1, 7, 9], [0.25, 2.0, -0.5])]
        self.v_cols = [([1], [0.75]),
                       ([3, 5], [1.0, -0.25]),
                       ([0, 8], [-1.0, 0.5])]
        U = [dense_from_sparse(self.n, i, v) for i, v in self.u_cols]
        V = [dense_from_sparse(self.n, i, v) for i, v in self.v_cols]
        self.a_upd = [[self.a[i][j] + sum(U[c][i] * V[c][j]
                                          for c in range(self.k))
                       for j in range(self.n)] for i in range(self.n)]

    def test_matches_direct_dense_solve(self):
        upd = LowRankUpdate(self.lu, self.u_cols, self.v_cols,
                            self.n, self.k)
        b = [self.rng.uniform(-3, 3) for _ in range(self.n)]
        x_upd = upd.solve(b)
        x_ref = LU(self.a_upd, self.n).solve(b)
        self.assertLess(max_err(x_upd, x_ref), 1e-9)
        self.assertLess(residual(self.a_upd, x_upd, b), 1e-9)

    def test_multiple_rhs_consistent_with_reference(self):
        upd = LowRankUpdate(self.lu, self.u_cols, self.v_cols,
                            self.n, self.k)
        ref = LU(self.a_upd, self.n)
        rhs = [[self.rng.uniform(-2, 2) for _ in range(self.n)]
               for _ in range(6)]
        for x_upd, x_ref in zip(upd.solve_many(rhs), ref.solve_many(rhs)):
            self.assertLess(max_err(x_upd, x_ref), 1e-9)

    def test_singular_low_rank_update_detected_and_base_survives(self):
        # U = [-A e_1], V = [e_1] makes column 1 of A + U V^T zero.
        n = self.n
        col1 = [self.a[i][1] for i in range(n)]
        u_cols = [(list(range(n)), [-c for c in col1])]
        v_cols = [([1], [1.0])]
        with self.assertRaises(SingularUpdateError):
            LowRankUpdate(self.lu, u_cols, v_cols, n, 1)
        b = [self.rng.uniform(-1, 1) for _ in range(n)]
        x = self.lu.solve(b)  # base factorization still usable
        self.assertLess(residual(self.a, x, b), 1e-10)

    def test_rank_one_is_special_case_of_low_rank(self):
        u_idx, u_val = [2, 6], [1.0, -0.5]
        v_idx, v_val = [0, 4], [0.5, 1.5]
        r1 = RankOneUpdate(self.lu, u_idx, u_val, v_idx, v_val, self.n)
        lr = LowRankUpdate(self.lu, [(u_idx, u_val)], [(v_idx, v_val)],
                           self.n, 1)
        b = [self.rng.uniform(-2, 2) for _ in range(self.n)]
        self.assertLess(max_err(r1.solve(b), lr.solve(b)), 1e-12)

    def test_reverse_update_via_composition(self):
        # LowRankUpdate composes too: undo a rank-1 with (-u, v) on top.
        u_idx, u_val = [3], [2.0]
        v_idx, v_val = [5], [1.0]
        upd = LowRankUpdate(self.lu, [(u_idx, u_val)], [(v_idx, v_val)],
                            self.n, 1)
        back = RankOneUpdate(upd, u_idx, [-2.0], v_idx, v_val, self.n)
        b = [self.rng.uniform(-2, 2) for _ in range(self.n)]
        self.assertLess(max_err(back.solve(b), self.lu.solve(b)), 1e-8)


if __name__ == "__main__":
    unittest.main(verbosity=2)
