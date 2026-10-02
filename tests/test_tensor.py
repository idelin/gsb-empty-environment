import unittest

from minivmap import tensor as T


class TestTensor(unittest.TestCase):
    def test_broadcast_add(self):
        a = T.Tensor.from_nested([[1, 2, 3], [4, 5, 6]])
        b = T.Tensor.from_nested([10, 20, 30])
        c = T.add(a, b)
        self.assertEqual(c.shape, (2, 3))
        self.assertEqual(c.to_nested(), [[11, 22, 33], [14, 25, 36]])

    def test_transpose_reshape_reduce(self):
        a = T.Tensor.from_nested([[1, 2, 3], [4, 5, 6]])
        t = T.transpose(a, (1, 0))
        self.assertEqual(t.to_nested(), [[1, 4], [2, 5], [3, 6]])
        r = T.reshape(t, (6,))
        self.assertEqual(r.data, [1, 4, 2, 5, 3, 6])
        self.assertEqual(T.reduce_sum(a, 0).data, [5, 7, 9])
        self.assertEqual(T.reduce_mean(a, 1).data, [2, 5])

    def test_matmul_batched_matches_2d_slices(self):
        a = T.Tensor.randn((2, 3, 4), seed=0)
        b = T.Tensor.randn((4, 5), seed=1)
        c = T.matmul(a, b)
        self.assertEqual(c.shape, (2, 3, 5))
        for i in range(2):
            ci = T.matmul(T.slice_along(a, 0, i), b)
            self.assertLessEqual(T.max_abs_diff(T.slice_along(c, 0, i), ci), 1e-12)

    def test_moveaxis_stack_slice(self):
        a = T.Tensor.randn((2, 3, 4), seed=2)
        m = T.moveaxis(a, 2, 0)
        self.assertEqual(m.shape, (4, 2, 3))
        s = T.slice_along(m, 0, 1)
        self.assertEqual(s.shape, (2, 3))
        st = T.stack([s, s], axis=0)
        self.assertEqual(st.shape, (2, 2, 3))
        self.assertLessEqual(T.max_abs_diff(T.slice_along(st, 0, 0), s), 0.0)

    def test_unbroadcast(self):
        g = T.Tensor.ones((3, 4))
        self.assertEqual(T.unbroadcast(g, (4,)).data, [3, 3, 3, 3])
        self.assertEqual(T.unbroadcast(g, (1, 4)).shape, (1, 4))
        self.assertEqual(T.unbroadcast(g, ()).data, [12.0])


if __name__ == "__main__":
    unittest.main()
