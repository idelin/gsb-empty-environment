"""Tests for dropout, checkpoint recomputation, and mask-correct gradients."""

import unittest

from keyrng import (
    Tensor,
    dropout,
    dropout_backward,
    dropout_checkpoint_backward,
    dropout_checkpoint_forward,
    dropout_mask,
    key,
    uniform,
)


class TestDropout(unittest.TestCase):
    def test_mask_values_and_keep_fraction(self):
        rate = 0.4
        mask = dropout_mask(key(10), (20000,), rate)
        keep_scale = 1.0 / (1.0 - rate)
        self.assertTrue(all(m in (0.0, keep_scale) for m in mask.data))
        kept = sum(1 for m in mask.data if m != 0.0) / len(mask.data)
        self.assertAlmostEqual(kept, 1.0 - rate, delta=0.02)

    def test_forward_applies_mask(self):
        x = uniform(key(1), (64,))
        k = key(2)
        y, mask = dropout(x, k, 0.5)
        for xi, yi, mi in zip(x.data, y.data, mask.data):
            self.assertEqual(yi, xi * mi)

    def test_same_key_reproduces_mask(self):
        k = key(3)
        self.assertTrue(
            dropout_mask(k, (128,), 0.3).exact_equal(dropout_mask(k, (128,), 0.3))
        )

    def test_gradient_flows_only_through_kept_units(self):
        x = uniform(key(11), (256,))
        dy = uniform(key(12), (256,))
        _, mask = dropout(x, key(13), 0.5)
        dx = dropout_backward(dy, mask)
        for dxi, dyi, mi in zip(dx.data, dy.data, mask.data):
            self.assertEqual(dxi, dyi * mi)
            if mi == 0.0:
                self.assertEqual(dxi, 0.0)

    def test_invalid_rate(self):
        x = Tensor.zeros((4,))
        for bad in (-0.1, 1.0, 1.5, float("nan")):
            with self.assertRaises(ValueError):
                dropout(x, key(1), bad)

    def test_empty_tensor(self):
        x = Tensor.zeros((0,))
        y, mask = dropout(x, key(1), 0.5)
        self.assertEqual(y.data, [])
        self.assertEqual(mask.data, [])


class TestCheckpointRecomputation(unittest.TestCase):
    def test_recomputed_mask_is_identical_not_resampled(self):
        x = uniform(key(21), (1000,))
        k = key(22)
        y_fwd, mask = dropout(x, k, 0.45)
        y_ckpt, token = dropout_checkpoint_forward(x, k, 0.45)
        recomputed = token.recompute_mask()
        self.assertTrue(recomputed.exact_equal(mask))
        self.assertTrue(y_ckpt.exact_equal(y_fwd))
        # A different key gives a different mask: recomputation genuinely
        # reuses the original draw, not just any draw.
        other = dropout_mask(key(23), (1000,), 0.45)
        self.assertFalse(other.exact_equal(mask))

    def test_checkpoint_backward_uses_original_mask(self):
        x = uniform(key(31), (512,))
        dy = uniform(key(32), (512,))
        _, token = dropout_checkpoint_forward(x, key(33), 0.6)
        dx_ckpt = dropout_checkpoint_backward(dy, token)
        mask = token.recompute_mask()
        for dxi, dyi, mi in zip(dx_ckpt.data, dy.data, mask.data):
            self.assertEqual(dxi, dyi * mi)

    def test_checkpoint_token_is_lightweight(self):
        # The token stores key + shape + rate only: no mask tensor.
        _, token = dropout_checkpoint_forward(Tensor.zeros((8,)), key(1), 0.25)
        self.assertEqual(token.shape, (8,))
        self.assertEqual(token.rate, 0.25)
        self.assertFalse(hasattr(token, "mask"))


if __name__ == "__main__":
    unittest.main()
