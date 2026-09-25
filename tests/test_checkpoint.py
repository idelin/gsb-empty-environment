import unittest

import mini_autograd as ma


def _segment(a, b):
    h = ma.relu(ma.add(a, b))
    return ma.sum(ma.mul(h, h))


class TestCheckpoint(unittest.TestCase):
    def test_checkpoint_numeric(self):
        a = ma.tensor([0.8, -0.5, 1.3], requires_grad=True)
        b = ma.tensor([-0.3, 0.9, -0.4], requires_grad=True)

        def build():
            return ma.checkpoint(_segment, a, b, seed=42)

        err = ma.gradcheck(build, [a, b], tol=1e-5)
        self.assertLess(err, 1e-6)

    def test_checkpoint_saves_no_inner_nodes(self):
        with ma.Graph("outer").use() as g:
            a = ma.tensor([1.0, -2.0, 3.0], requires_grad=True)
            b = ma.tensor([0.5, 0.5, 0.5], requires_grad=True)
            y = ma.checkpoint(_segment, a, b, seed=1)
            outer_nodes = list(g.nodes)
            y.backward(retain_graph=True)
        self.assertEqual(len(outer_nodes), 1)
        self.assertEqual(outer_nodes[0].metadata.get("kind"), "recompute")
        self.assertEqual(a.grad.data, [3.0, 0.0, 7.0])

    def test_random_key_replays_dropout(self):
        def stochastic(x):
            with ma.rng_key(99):
                return ma.sum(ma.dropout(x, 0.5))

        with ma.Graph().use():
            x = ma.tensor([0.3, 0.7, 0.2, 0.9], requires_grad=True)
            y = ma.checkpoint(stochastic, x, seed=99)
            y.backward()

        from mini_autograd.rng import random_unit

        with ma.rng_key(99):
            mask = [1 if random_unit() >= 0.5 else 0 for _ in range(4)]
        expected = [2.0 * bit for bit in mask]
        self.assertEqual(x.grad.data, expected)

    def test_mutated_input_aborts_recompute(self):
        with ma.Graph().use():
            a = ma.tensor([1.0, 2.0], requires_grad=True)
            b = ma.tensor([3.0, 4.0], requires_grad=True)
            y = ma.checkpoint(_segment, a, b, seed=5)
            a.add_([10.0, 10.0])
            with self.assertRaises(ma.VersionMismatchError):
                y.backward()
        self.assertIsNone(a.grad)
        self.assertIsNone(b.grad)


if __name__ == "__main__":
    unittest.main()
