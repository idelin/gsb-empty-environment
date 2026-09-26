import unittest

from tensorfusion.tensor import (Tensor, align_strides, broadcast_shape,
                                 project_index)


class TestBroadcast(unittest.TestCase):
    def test_broadcast_shape(self):
        self.assertEqual(broadcast_shape((2, 3), (3,)), (2, 3))
        self.assertEqual(broadcast_shape((), (2, 1), (1, 4)), (2, 4))
        self.assertEqual(broadcast_shape(()), ())
        with self.assertRaises(ValueError):
            broadcast_shape((2,), (3,))

    def test_align_and_project(self):
        st = align_strides((3,), (1,), (2, 3))
        self.assertEqual(st, (0, 1))
        self.assertEqual(project_index((1, 2), (3,), st), (2,))
        st = align_strides((2, 1), (3, 1), (2, 4))
        self.assertEqual(st, (3, 0))
        self.assertEqual(project_index((1, 3), (2, 1), st), (1, 0))


class TestViewsAndVersions(unittest.TestCase):
    def test_transpose_shares_storage(self):
        x = Tensor.from_values([1, 2, 3, 4, 5, 6], (2, 3), name="x")
        xt = x.transpose()
        self.assertTrue(x.shares_storage(xt))
        self.assertEqual(xt.shape, (3, 2))
        self.assertEqual(xt.get((2, 1)), 6.0)
        self.assertEqual(xt.flat_pos((1, 0)), x.flat_pos((0, 1)))

    def test_narrow_offset(self):
        x = Tensor.from_values([1, 2, 3, 4, 5, 6], (2, 3))
        col = x.narrow(1, 1, 2)
        self.assertEqual(col.shape, (2, 2))
        self.assertEqual(col.to_flat(), [2.0, 3.0, 5.0, 6.0])

    def test_broadcast_to_is_stride_zero_view(self):
        x = Tensor.from_values([7, 8, 9], (3,))
        v = x.broadcast_to((2, 3))
        self.assertEqual(v.strides, (0, 1))
        self.assertEqual(v.to_flat(), [7.0, 8.0, 9.0, 7.0, 8.0, 9.0])

    def test_version_bumps_only_on_mutation(self):
        x = Tensor.from_values([1, 2], (2,))
        self.assertEqual(x.version, 0)
        x.set((0,), 5.0)
        self.assertEqual(x.version, 1)
        x._write((1,), 9.0)
        self.assertEqual(x.version, 1)
        xt = x.transpose()
        self.assertEqual(xt.version, x.version)


if __name__ == "__main__":
    unittest.main()
