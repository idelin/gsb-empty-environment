import unittest

from tinyreduce import Tensor


class TestConstruction(unittest.TestCase):
    def test_nested_shape_inference(self):
        t = Tensor([[1, 2, 3], [4, 5, 6]])
        self.assertEqual(t.shape, (2, 3))
        self.assertEqual(t.to_flat(), [1.0, 2.0, 3.0, 4.0, 5.0, 6.0])

    def test_scalar(self):
        t = Tensor(2.5)
        self.assertEqual(t.shape, ())
        self.assertEqual(t.item(), 2.5)

    def test_empty_dims(self):
        self.assertEqual(Tensor([]).shape, (0,))
        self.assertEqual(Tensor([[], []]).shape, (2, 0))
        self.assertEqual(Tensor.zeros((2, 0, 3)).size, 0)

    def test_ragged_rejected(self):
        with self.assertRaises(ValueError):
            Tensor([[1, 2], [3]])

    def test_get_set(self):
        t = Tensor.zeros((2, 2))
        t.set((1, 0), 7.0)
        self.assertEqual(t.get((1, 0)), 7.0)
        self.assertEqual(t.to_nested(), [[0.0, 0.0], [7.0, 0.0]])


class TestViews(unittest.TestCase):
    def setUp(self):
        self.t = Tensor([[1, 2, 3], [4, 5, 6]])

    def test_int_index(self):
        self.assertEqual(self.t[1].to_flat(), [4.0, 5.0, 6.0])
        self.assertEqual(self.t[-1, 0].item(), 4.0)
        with self.assertRaises(IndexError):
            self.t[5]

    def test_slices_and_negative_strides(self):
        self.assertEqual(self.t[:, 1].to_flat(), [2.0, 5.0])
        self.assertEqual(self.t[::-1].to_nested(), [[4.0, 5.0, 6.0],
                                                    [1.0, 2.0, 3.0]])
        self.assertEqual(self.t[:, ::2].to_nested(), [[1.0, 3.0], [4.0, 6.0]])
        self.assertEqual(self.t[::-1, ::-1].to_nested(),
                         [[6.0, 5.0, 4.0], [3.0, 2.0, 1.0]])

    def test_ellipsis(self):
        t = Tensor.zeros((2, 3, 4))
        self.assertEqual(t[..., 0].shape, (2, 3))
        self.assertEqual(t[0, ...].shape, (3, 4))

    def test_views_share_storage(self):
        v = self.t[::-1]
        v.set((0, 0), 99.0)
        self.assertEqual(self.t.get((1, 0)), 99.0)

    def test_permute_transpose(self):
        p = self.t.permute((1, 0))
        self.assertEqual(p.shape, (3, 2))
        self.assertEqual(p.to_nested(), [[1.0, 4.0], [2.0, 5.0], [3.0, 6.0]])
        self.assertEqual(self.t.T.to_nested(), p.to_nested())
        with self.assertRaises(ValueError):
            self.t.permute((0, 0))

    def test_reshape(self):
        r = self.t.reshape((3, 2))
        self.assertEqual(r.to_nested(), [[1.0, 2.0], [3.0, 4.0], [5.0, 6.0]])
        with self.assertRaises(ValueError):
            self.t[::-1].reshape((6,))  # non-contiguous
        self.assertEqual(self.t[::-1].contiguous().reshape((6,)).shape, (6,))

    def test_clone_is_deep(self):
        c = self.t.clone()
        c.set((0, 0), -1.0)
        self.assertEqual(self.t.get((0, 0)), 1.0)


if __name__ == "__main__":
    unittest.main()
