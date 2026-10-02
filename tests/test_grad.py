import unittest

from minivmap import tensor as T
from minivmap.batch import batch_transform
from minivmap.grad import value_and_grad
from minivmap.graph import GraphBuilder
from minivmap.verify import (compare_batched_vs_loop, finite_difference_grad,
                             per_sample_grads)
from tests.test_batch import IN_AXES, WRT, build_mlp, mlp_env


class TestGrad(unittest.TestCase):
    def test_hand_computed_shared_param_grad_sums_across_samples(self):
        # loss = sum(x * w); d loss / d w = x (per sample)
        g = GraphBuilder()
        x = g.input("x", (2,))
        w = g.input("w", (2,))
        loss = g.reduce_sum(g.mul(x, w), axis=0)
        single = g.build(outputs=[loss])
        env = {"x": T.Tensor.from_nested([[1, 2], [3, 4], [5, 6]]),
               "w": T.Tensor.from_nested([7, 8])}
        shapes = {k: v.shape for k, v in env.items()}
        batched, _ = batch_transform(single, {"x": 0, "w": None}, shapes)
        _, grads = value_and_grad(batched, ["w", "x"], env)
        # shared param grad = sum of per-sample grads = column sums of x
        self.assertEqual(grads["w"].data, [9.0, 12.0])
        # mapped input grad: each row equals w
        self.assertEqual(grads["x"].to_nested(), [[7, 8], [7, 8], [7, 8]])

    def test_single_sample_grad_matches_finite_differences(self):
        single = build_mlp()
        env = mlp_env(1)
        env = {k: (T.slice_along(v, 0, 0) if k in ("x", "y") else v)
               for k, v in env.items()}
        _, grads = value_and_grad(single, ["w1", "b1", "w2", "x"], env)
        fd = finite_difference_grad(single, ["w1", "b1", "w2", "x"], env)
        for w in fd:
            self.assertLessEqual(T.max_abs_diff(grads[w], fd[w]), 1e-4, w)

    def test_batched_grad_matches_finite_differences(self):
        # independent numerical verification of the batched reverse mode
        single = build_mlp()
        env = mlp_env(4)
        shapes = {k: v.shape for k, v in env.items()}
        batched, _ = batch_transform(single, IN_AXES, shapes)
        _, grads = value_and_grad(batched, ["w1", "b1", "w2"], env)
        fd = finite_difference_grad(batched, ["w1", "b1", "w2"], env)
        for w in fd:
            self.assertLessEqual(T.max_abs_diff(grads[w], fd[w]), 1e-4, w)

    def test_shared_grad_is_sum_of_per_sample_grads(self):
        single = build_mlp()
        env = mlp_env(6)
        shapes = {k: v.shape for k, v in env.items()}
        batched, _ = batch_transform(single, IN_AXES, shapes)
        _, batched_grads = value_and_grad(batched, WRT, env)
        loop_grads = per_sample_grads(single, IN_AXES, env, WRT)
        for w in WRT:
            self.assertLessEqual(T.max_abs_diff(batched_grads[w], loop_grads[w]),
                                 1e-9, w)

    def test_nested_grad_matches_loop(self):
        single = build_mlp()
        env1 = mlp_env(3)
        shapes1 = {k: v.shape for k, v in env1.items()}
        level1, _ = batch_transform(single, IN_AXES, shapes1)
        env2 = dict(env1)
        env2["x"] = T.Tensor.randn((2, 3, 3), seed=21)
        env2["y"] = T.Tensor.randn((2, 3, 1), seed=22)
        shapes2 = {k: v.shape for k, v in env2.items()}
        level2, _ = batch_transform(level1, {"x": 0, "y": 0}, shapes2)
        report = compare_batched_vs_loop(level1, {"x": 0, "y": 0}, env2,
                                         WRT, level2)
        self.assertTrue(report["ok"], report)


if __name__ == "__main__":
    unittest.main()
