import unittest

from checkpoint.rng import Rng
from checkpoint.tensor import Tensor, VersionError, add, matmul, mul
from checkpoint.tensor import relu, sum_all, tanh, transpose


class TestTensorOps(unittest.TestCase):
    def test_elementwise(self):
        a = Tensor([1.0, 2.0, 3.0, 4.0], (2, 2))
        b = Tensor([5.0, 6.0, 7.0, 8.0], (2, 2))
        self.assertEqual(add(a, b).data(), [6.0, 8.0, 10.0, 12.0])
        self.assertEqual(mul(a, b).data(), [5.0, 12.0, 21.0, 32.0])

    def test_matmul(self):
        a = Tensor([1.0, 2.0, 3.0, 4.0, 5.0, 6.0], (2, 3))
        b = Tensor([1.0, 0.0, 0.0, 1.0, 1.0, 1.0], (3, 2))
        self.assertEqual(matmul(a, b).data(), [4.0, 5.0, 10.0, 11.0])
        t = transpose(a)
        self.assertEqual(t.shape, (3, 2))
        self.assertEqual(t.data(), [1.0, 4.0, 2.0, 5.0, 3.0, 6.0])

    def test_nonlinear_and_reduce(self):
        import math
        a = Tensor([-1.0, 2.0], (1, 2))
        self.assertEqual(relu(a).data(), [0.0, 2.0])
        self.assertEqual(sum_all(a).data(), [1.0])
        self.assertEqual(sum_all(a).shape, ())
        self.assertAlmostEqual(tanh(a).data()[0], math.tanh(-1.0))

    def test_randn_deterministic(self):
        t1 = Tensor.randn((2, 3), Rng(7))
        t2 = Tensor.randn((2, 3), Rng(7))
        self.assertEqual(t1.data(), t2.data())


class TestViews(unittest.TestCase):
    def test_view_identity(self):
        t = Tensor([1.0, 2.0, 3.0, 4.0], (2, 2))
        v = t.view()
        self.assertTrue(v.is_view_of(t))
        self.assertFalse(v.is_view_of(t.clone()))
        self.assertEqual(v.data(), t.data())

    def test_version_check_on_stale_view(self):
        t = Tensor([1.0, 2.0, 3.0, 4.0], (2, 2))
        v = t.view()
        t.add_(Tensor.full((2, 2), 1.0))
        self.assertEqual(t.data(), [2.0, 3.0, 4.0, 5.0])
        with self.assertRaises(VersionError):
            v.data()

    def test_reshape_view(self):
        t = Tensor([1.0, 2.0, 3.0, 4.0], (2, 2))
        v = t.view((4,))
        self.assertTrue(v.is_view_of(t))
        self.assertEqual(v.shape, (4,))


if __name__ == "__main__":
    unittest.main()
