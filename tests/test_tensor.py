import unittest

from dualtag import tensor
from dualtag.errors import SingularMatrixError


class TestTensor(unittest.TestCase):
    def test_matmul(self):
        A = [[1.0, 2.0], [3.0, 4.0]]
        B = [[5.0, 6.0], [7.0, 8.0]]
        self.assertEqual(tensor.matmul(A, B), [[19.0, 22.0], [43.0, 50.0]])

    def test_matvec_and_transpose(self):
        A = [[1.0, 2.0], [3.0, 4.0]]
        self.assertEqual(tensor.matvec(A, [1.0, 1.0]), [3.0, 7.0])
        self.assertEqual(tensor.transpose(A), [[1.0, 3.0], [2.0, 4.0]])

    def test_identity_zeros_norm(self):
        self.assertEqual(tensor.identity(2), [[1.0, 0.0], [0.0, 1.0]])
        self.assertEqual(tensor.zeros(1, 2), [[0.0, 0.0]])
        self.assertEqual(tensor.norm_inf([1.0, -3.0, 2.0]), 3.0)

    def test_solve(self):
        x = tensor.solve([[2.0, 1.0], [1.0, 3.0]], [1.0, 2.0])
        self.assertAlmostEqual(x[0], 0.2)
        self.assertAlmostEqual(x[1], 0.6)

    def test_solve_with_pivoting(self):
        x = tensor.solve([[0.0, 1.0], [1.0, 0.0]], [2.0, 3.0])
        self.assertAlmostEqual(x[0], 3.0)
        self.assertAlmostEqual(x[1], 2.0)

    def test_singular_raises(self):
        with self.assertRaises(SingularMatrixError):
            tensor.solve([[1.0, 2.0], [2.0, 4.0]], [1.0, 2.0])


if __name__ == "__main__":
    unittest.main()
