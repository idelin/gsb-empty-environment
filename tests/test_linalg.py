import unittest

from randomized_response.linalg import (
    jacobi_eigen,
    mat_mul,
    mat_vec,
    project_to_simplex,
    solve,
    SingularMatrixError,
)


class TestSolve(unittest.TestCase):
    def test_solves_well_conditioned_system(self):
        a = [[3.0, 1.0], [1.0, 2.0]]
        b = [9.0, 8.0]
        x = solve(a, b)
        self.assertAlmostEqual(x[0], 2.0)
        self.assertAlmostEqual(x[1], 3.0)

    def test_singular_raises(self):
        with self.assertRaises(SingularMatrixError):
            solve([[1.0, 1.0], [1.0, 1.0]], [1.0, 2.0])


class TestJacobiEigen(unittest.TestCase):
    def test_recovers_known_eigensystem(self):
        # Symmetric matrix with eigenvalues 1 and 3.
        a = [[2.0, 1.0], [1.0, 2.0]]
        values, vectors = jacobi_eigen(a)
        self.assertAlmostEqual(values[0], 3.0, places=10)
        self.assertAlmostEqual(values[1], 1.0, places=10)
        # A v = lambda v for each pair.
        for lam, vec in zip(values, vectors):
            av = mat_vec(a, vec)
            for avi, lvi in zip(av, [lam * vi for vi in vec]):
                self.assertAlmostEqual(avi, lvi, places=10)

    def test_rank_deficient_matrix_has_zero_eigenvalue(self):
        # Rank-1 matrix (like an epsilon=0 channel Gram matrix).
        a = [[1.0, 1.0, 1.0]] * 3
        values, _ = jacobi_eigen(a)
        self.assertAlmostEqual(values[0], 3.0, places=10)
        self.assertAlmostEqual(values[1], 0.0, places=10)
        self.assertAlmostEqual(values[2], 0.0, places=10)


class TestSimplexProjection(unittest.TestCase):
    def test_projection_is_on_simplex(self):
        for y in ([1.2, -0.1, -0.1], [0.2, 0.3, 0.5], [-5.0, 2.0, 4.0], [0.0] * 4):
            x = project_to_simplex(list(y))
            self.assertAlmostEqual(sum(x), 1.0, places=12)
            self.assertTrue(all(xi >= 0.0 for xi in x))

    def test_projection_is_closest_point(self):
        y = [1.5, -0.2, -0.3]
        x = project_to_simplex(y)
        # Known analytic projection: subtract theta = (1.5 - 1)/1? -> [1.0, 0.0, 0.0]
        self.assertAlmostEqual(x[0], 1.0, places=12)
        self.assertAlmostEqual(x[1], 0.0, places=12)
        self.assertAlmostEqual(x[2], 0.0, places=12)

    def test_k1_projects_to_one(self):
        self.assertEqual(project_to_simplex([7.0]), [1.0])


class TestMatMul(unittest.TestCase):
    def test_mat_mul_identity(self):
        a = [[1.0, 2.0], [3.0, 4.0]]
        self.assertEqual(mat_mul(a, [[1.0, 0.0], [0.0, 1.0]]), a)


if __name__ == "__main__":
    unittest.main()
