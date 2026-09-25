import math
import unittest

import mini_autograd as ma


class TestFiniteGraphs(unittest.TestCase):
    def test_elementwise_and_reductions_numeric(self):
        leaf_x = ma.tensor([0.7, -0.3, 1.5], requires_grad=True)
        leaf_y = ma.tensor([-0.4, 0.9, 0.2], requires_grad=True)

        def build():
            x, y = leaf_x, leaf_y
            z = ma.sum(
                ma.add(
                    ma.mul(x, y),
                    ma.exp(ma.mul(ma.tensor(0.5), x)),
                )
            )
            z = ma.add(z, ma.mean(ma.relu(ma.add(y, ma.tensor(0.2)))))
            return ma.add(z, ma.sum(ma.log(ma.add(x, ma.tensor(2.0)))))

        err = ma.gradcheck(build, [leaf_x, leaf_y], tol=1e-5)
        self.assertLess(err, 1e-6)

    def test_pow_div_sub_neg_numeric(self):
        leaf = ma.tensor([0.8, 1.3, 2.1], requires_grad=True)

        def build():
            a = leaf
            return ma.sum(ma.div(ma.pow_scalar(a, 2.5), ma.add(a, ma.tensor(1.0))))

        err = ma.gradcheck(build, [leaf], tol=1e-5)
        self.assertLess(err, 1e-6)

    def test_matmul_numeric(self):
        A = ma.tensor([[0.4, -0.9], [1.1, 0.2]], shape=(2, 2), requires_grad=True)
        B = ma.tensor([[0.7, 0.3, -0.5], [-0.2, 0.8, 0.6]], shape=(2, 3),
                      requires_grad=True)

        def build():
            return ma.sum(ma.matmul(A, B))

        err = ma.gradcheck(build, [A, B], tol=1e-5)
        self.assertLess(err, 1e-6)

    def test_matmul_shapes_and_values(self):
        with ma.Graph().use():
            A = ma.tensor([[1.0, 2.0], [3.0, 4.0]], shape=(2, 2), requires_grad=True)
            B = ma.tensor([[5.0, 6.0], [7.0, 8.0]], shape=(2, 2), requires_grad=True)
            C = ma.matmul(A, B)
            self.assertEqual(C.shape, (2, 2))
            self.assertEqual(C.data, [19.0, 22.0, 43.0, 50.0])
            ma.sum(C).backward()
            # d(sum(AB))/dA = ones(1x2) @ B^T: per j, sum_t B_jt (row sums of B)
            self.assertEqual(A.grad.data, [11.0, 15.0, 11.0, 15.0])
            # d(sum(AB))/dB = A^T @ ones: column sums of A = [4,4,6,6]
            self.assertEqual(B.grad.data, [4.0, 4.0, 6.0, 6.0])


class TestSharedNodes(unittest.TestCase):
    def test_shared_node_runs_backward_once(self):
        with ma.Graph().use() as g:
            x = ma.tensor([1.0, 2.0, 3.0], requires_grad=True)
            shared = ma.relu(x)                       # one node, used twice
            y = ma.add(ma.sum(shared), ma.sum(shared))  # two consumers
            y.backward()
        self.assertEqual(x.grad.data, [2.0, 2.0, 2.0])

    def test_shared_node_run_count_is_one(self):
        with ma.Graph().use() as g:
            x = ma.tensor([1.0, 2.0, 3.0], requires_grad=True)
            shared = ma.relu(x)
            y = ma.add(ma.sum(shared), ma.mul(ma.sum(shared), ma.tensor(1.0)))
            node = shared.grad_fn
            y.backward(retain_graph=True)
            self.assertEqual(node.run_count, 1)

    def test_diamond_dependency_deduplicated(self):
        with ma.Graph().use():
            x = ma.tensor([1.0], requires_grad=True)
            a = ma.pow_scalar(x, 2.0)
            b = ma.pow_scalar(x, 3.0)
            y = ma.add(a, b)
            ma.sum(y).backward()
        # dy/dx = 2x + 3x^2 = 2 + 3 = 5
        self.assertAlmostEqual(x.grad.data[0], 5.0, places=9)


class TestRootValidation(unittest.TestCase):
    def test_non_scalar_requires_explicit_gradient(self):
        with ma.Graph().use():
            x = ma.tensor([1.0, 2.0], requires_grad=True)
            y = ma.add(x, x)
            with self.assertRaises(ma.InvalidBackwardError):
                y.backward()
            y.backward(ma.tensor([1.0, 1.0]), retain_graph=True)
            self.assertEqual(x.grad.data, [2.0, 2.0])


if __name__ == "__main__":
    unittest.main()
