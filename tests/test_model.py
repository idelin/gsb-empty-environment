"""Independent numerical verification of the hand-written gradients via
central finite differences."""

import unittest

from gradacc.model import init_params, loss_and_grad


class ModelGradCheckTest(unittest.TestCase):
    def test_analytic_grad_matches_finite_difference(self):
        params = init_params(in_dim=3, out_dim=2, seed=1)
        samples = [([0.3, -0.7, 1.2], [1.0, -1.0]),
                   ([-0.4, 0.9, 0.2], [0.5, 2.0]),
                   ([1.1, 0.0, -0.6], [-0.3, 0.8])]
        _, grads = loss_and_grad(params, samples)
        eps = 1e-6
        for name in ("w", "b"):
            for i in range(params[name].size):
                plus = {k: v.copy() for k, v in params.items()}
                minus = {k: v.copy() for k, v in params.items()}
                plus[name].data[i] += eps
                minus[name].data[i] -= eps
                loss_plus, _ = loss_and_grad(plus, samples)
                loss_minus, _ = loss_and_grad(minus, samples)
                numeric = (loss_plus - loss_minus) / (2.0 * eps)
                self.assertAlmostEqual(
                    numeric, grads[name].data[i], places=6,
                    msg=f"grad mismatch for {name}[{i}]")

    def test_microbatch_grads_weight_to_full_batch(self):
        # mean of sample grads equals the full-batch mean gradient
        params = init_params(in_dim=2, out_dim=1, seed=3)
        samples = [([1.0, 2.0], [1.0]), ([-1.0, 0.5], [0.0]),
                   ([0.5, -0.5], [2.0])]
        _, full = loss_and_grad(params, samples)
        acc = {k: v.copy().scale(0.0) for k, v in full.items()}
        for sample in samples:
            _, g = loss_and_grad(params, [sample])
            for name in acc:
                for i, v in enumerate(g[name].data):
                    acc[name].data[i] += v / len(samples)
        for name in acc:
            self.assertTrue(full[name].almost_equal(acc[name]),
                            msg=f"{name}: {full[name]} vs {acc[name]}")


if __name__ == "__main__":
    unittest.main()
