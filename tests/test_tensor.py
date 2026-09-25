import unittest

from tinymat import Tensor


class TestTensorViews(unittest.TestCase):
    def setUp(self):
        self.base = Tensor.from_nested([[1, 2, 3], [4, 5, 6]])

    def test_from_nested_roundtrip(self):
        self.assertEqual(self.base.shape, (2, 3))
        self.assertEqual(self.base.to_nested(), [[1.0, 2.0, 3.0],
                                                 [4.0, 5.0, 6.0]])
        self.assertEqual(Tensor.from_nested(7).to_nested(), 7.0)
        self.assertEqual(Tensor.from_nested([[], []]).shape, (2, 0))

    def test_transpose_is_stride_permutation(self):
        t = self.base.transpose(1, 0)
        self.assertEqual(t.shape, (3, 2))
        self.assertEqual(t.strides, (1, 3))
        self.assertEqual(t.to_nested(), [[1.0, 4.0], [2.0, 5.0], [3.0, 6.0]])
        self.assertIs(t.data, self.base.data)  # no copy

    def test_slice_with_step_and_negative_step(self):
        big = Tensor.from_nested([[i * 4 + j for j in range(4)]
                                  for i in range(6)])
        s = big.slice_dim(0, 0, 6, 2)
        self.assertEqual(s.to_nested(), [[0.0, 1.0, 2.0, 3.0],
                                         [8.0, 9.0, 10.0, 11.0],
                                         [16.0, 17.0, 18.0, 19.0]])
        r = big.slice_dim(0, None, None, -1)
        self.assertEqual(r.to_nested()[0], [20.0, 21.0, 22.0, 23.0])
        c = big.slice_dim(1, 1, 4, 2)
        self.assertEqual(c.shape, (6, 2))
        self.assertEqual(c.value_at((2, 1)), 11.0)

    def test_broadcast_to_uses_stride_zero(self):
        b = Tensor.from_nested([10, 20, 30]).broadcast_to((4, 3))
        self.assertEqual(b.strides, (0, 1))
        self.assertEqual(len(b.data), 3)  # buffer NOT expanded
        self.assertEqual(b.to_nested()[3], [10.0, 20.0, 30.0])
        scalar = Tensor.from_nested(5).broadcast_to((2, 2, 2))
        self.assertEqual(scalar.physical_cells(), [0])
        with self.assertRaises(ValueError):
            Tensor.from_nested([1, 2, 3]).broadcast_to((4,))

    def test_composed_axis_transforms(self):
        # transpose -> strided slice -> broadcast, all views on one buffer
        v = (self.base.transpose(1, 0)      # (3,2)
             .slice_dim(1, 0, 2, 1)         # (3,2)
             .broadcast_to((5, 3, 2)))
        self.assertEqual(v.shape, (5, 3, 2))
        self.assertEqual(v.value_at((4, 2, 1)), 6.0)
        self.assertEqual(len(v.data), 6)
        # physical sharing: distinct logical indices, same cell
        self.assertEqual(v.physical_index((0, 0, 0)),
                         v.physical_index((3, 0, 0)))

    def test_contiguous_copy(self):
        t = self.base.transpose(1, 0).contiguous()
        self.assertEqual(t.strides, (2, 1))
        self.assertEqual(t.to_nested(), [[1.0, 4.0], [2.0, 5.0], [3.0, 6.0]])


if __name__ == "__main__":
    unittest.main()
