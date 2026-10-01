import math
import unittest

from gradacc.tensor import Tensor


class TensorTest(unittest.TestCase):
    def test_zeros_and_shape(self):
        t = Tensor.zeros((2, 3))
        self.assertEqual(t.shape, (2, 3))
        self.assertEqual(t.data, [0.0] * 6)

    def test_from_nested_roundtrip(self):
        t = Tensor.from_nested([[1, 2], [3, 4]])
        self.assertEqual(t.shape, (2, 2))
        self.assertEqual(t.to_nested(), [[1.0, 2.0], [3.0, 4.0]])

    def test_arithmetic(self):
        a = Tensor([1, 2, 3], (3,))
        b = Tensor([4, 5, 6], (3,))
        self.assertEqual((a + b).data, [5.0, 7.0, 9.0])
        self.assertEqual((b - a).data, [3.0, 3.0, 3.0])
        self.assertEqual(a.scale(2.0).data, [2.0, 4.0, 6.0])
        with self.assertRaises(ValueError):
            _ = a + Tensor([1, 2], (2,))

    def test_matmul(self):
        a = Tensor.from_nested([[1, 2], [3, 4]])
        b = Tensor.from_nested([[5, 6], [7, 8]])
        self.assertEqual(a.matmul(b).to_nested(), [[19.0, 22.0], [43.0, 50.0]])

    def test_outer(self):
        a = Tensor([1, 2], (2,))
        b = Tensor([3, 4, 5], (3,))
        self.assertEqual(a.outer(b).to_nested(),
                         [[3.0, 4.0, 5.0], [6.0, 8.0, 10.0]])

    def test_isfinite(self):
        self.assertTrue(Tensor([1.0, 2.0], (2,)).isfinite())
        self.assertFalse(Tensor([1.0, float("inf")], (2,)).isfinite())
        self.assertFalse(Tensor([float("nan")], (1,)).isfinite())

    def test_json_roundtrip(self):
        t = Tensor.from_nested([[1.5, -2.5], [3.5, 4.5]])
        self.assertEqual(Tensor.from_json(t.to_json()).data, t.data)
        self.assertEqual(Tensor.from_json(t.to_json()).shape, t.shape)

    def test_almost_equal(self):
        a = Tensor([1.0, 2.0], (2,))
        b = Tensor([1.0 + 1e-13, 2.0], (2,))
        self.assertTrue(a.almost_equal(b))
        self.assertFalse(a.almost_equal(Tensor([1.0, 2.1], (2,))))


if __name__ == "__main__":
    unittest.main()
