"""End-to-end: chunked per-sample dropout with checkpointed backward.

Changing the batch chunking must not change any sample's random values,
and gradients must propagate through the *original* mask (recomputed
from the stored key), never through a freshly drawn one.
"""

import unittest

from keyrng import (
    Tensor,
    dropout_checkpoint_backward,
    dropout_checkpoint_forward,
    key,
    map_samples_chunked,
    uniform,
)


def hash_id_of(sample_id):
    return sum(ord(c) for c in sample_id)


class TestChunkedDropoutGradient(unittest.TestCase):
    def setUp(self):
        self.base = key(9001)
        self.ids = [f"id-{i}" for i in range(12)]
        self.dy = uniform(key(77), (16,))

    def run_pipeline(self, chunk_size):
        def fwd(k, sid):
            features = Tensor(
                [float((hash_id_of(sid) + j) % 7) for j in range(16)], (16,)
            )
            return dropout_checkpoint_forward(features, k, 0.4)

        results = map_samples_chunked(self.base, self.ids, fwd, chunk_size)
        grads = {
            sid: dropout_checkpoint_backward(self.dy, token)
            for sid, (_out, token) in results.items()
        }
        return results, grads

    def test_values_and_gradients_invariant_to_chunking(self):
        results_1, grads_1 = self.run_pipeline(1)
        results_5, grads_5 = self.run_pipeline(5)
        results_12, grads_12 = self.run_pipeline(12)
        for sid in self.ids:
            out1, _ = results_1[sid]
            out5, _ = results_5[sid]
            out12, _ = results_12[sid]
            self.assertTrue(out1.exact_equal(out5), sid)
            self.assertTrue(out1.exact_equal(out12), sid)
            self.assertTrue(grads_1[sid].exact_equal(grads_5[sid]), sid)
            self.assertTrue(grads_1[sid].exact_equal(grads_12[sid]), sid)

    def test_gradient_matches_original_mask(self):
        results, grads = self.run_pipeline(3)
        for sid in self.ids:
            _out, token = results[sid]
            mask = token.recompute_mask()
            for g, d, m in zip(grads[sid].data, self.dy.data, mask.data):
                self.assertEqual(g, d * m)
                if m == 0.0:
                    self.assertEqual(g, 0.0)


if __name__ == "__main__":
    unittest.main()
