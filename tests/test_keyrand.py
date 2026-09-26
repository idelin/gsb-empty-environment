"""keyrand 可运行测试：确定性、子流独立、checkpoint 重算、批处理分块、
断点续跑、键冲突、空张量、非法参数，以及独立数值验证。"""
import math
import unittest

from keyrand import (
    Checkpoint,
    Executor,
    Graph,
    KeyConflictError,
    Tensor,
    batched_map,
    bernoulli_tensor,
    dropout_backward,
    dropout_forward,
    fold_in,
    gaussian_reparam_backward,
    gaussian_reparam_sample,
    normal_tensor,
    root_key,
    sample_key,
    split,
    uniform_tensor,
)


class TestDeterminism(unittest.TestCase):
    def test_same_key_same_shape_identical(self):
        key = root_key(42)
        a = normal_tensor(key, (3, 4))
        b = normal_tensor(key, (3, 4))
        self.assertEqual(a, b)
        c = uniform_tensor(key, (3, 4))
        d = uniform_tensor(key, (3, 4))
        self.assertEqual(c, d)

    def test_different_keys_differ(self):
        a = normal_tensor(root_key(1), (8,))
        b = normal_tensor(root_key(2), (8,))
        self.assertNotEqual(a, b)

    def test_split_substreams_order_independent(self):
        root = root_key(7)
        keys = split(root, 4)
        # 正序生成
        forward = [normal_tensor(k, (5,)) for k in keys]
        # 逆序生成
        backward = [normal_tensor(k, (5,)) for k in reversed(keys)][::-1]
        self.assertEqual(forward, backward)
        # 子流两两不同
        for i in range(4):
            for j in range(i + 1, 4):
                self.assertNotEqual(forward[i], forward[j])
        # split 本身确定
        self.assertEqual(split(root, 4), keys)

    def test_fold_in_stable(self):
        root = root_key(0)
        self.assertEqual(fold_in(root, 123), fold_in(root, 123))
        self.assertNotEqual(fold_in(root, 123), fold_in(root, 124))


class TestDropoutCheckpoint(unittest.TestCase):
    def test_recompute_reuses_original_mask(self):
        key = root_key(99)
        x = Tensor.full((4, 5), 1.5)

        def forward(k, value):
            return dropout_forward(value, k, p=0.4)

        ckpt = Checkpoint(forward, key)
        out1, mask1 = ckpt.forward(x)
        out2, mask2 = ckpt.recompute(x)  # 重算必须复用原 mask
        self.assertEqual(mask1, mask2)
        self.assertEqual(out1, out2)
        # mask 取值只能是 0 或 1/(1-p)
        for v in mask1.data:
            self.assertIn(v, (0.0, 1.0 / 0.6))

    def test_gradient_follows_original_mask(self):
        key = root_key(11)
        x = Tensor(list(range(1, 13)), (3, 4))
        out, mask = dropout_forward(x, key, p=0.5)
        grad_out = Tensor.full((3, 4), 2.0)
        grad_x = dropout_backward(grad_out, mask)
        # 梯度恰好是 grad_out * mask：被丢弃处梯度为 0
        for g, m in zip(grad_x.data, mask.data):
            self.assertEqual(g, 2.0 * m)
        # 重算后 mask 不变 => 梯度也不变
        ckpt = Checkpoint(lambda k, v: dropout_forward(v, k, p=0.5), key)
        _, mask2 = ckpt.recompute(x)
        self.assertEqual(dropout_backward(grad_out, mask2), grad_x)


class TestBatchChunking(unittest.TestCase):
    SAMPLES = [(sid, float(sid)) for sid in (101, 7, 42, 900, 13, 256)]

    def _fn(self, key, value):
        eps = normal_tensor(key, (3,))
        return Tensor([value + e for e in eps.data], (3,))

    def test_chunking_preserves_per_sample_randomness(self):
        root = root_key(2024)
        baseline = batched_map(self._fn, root, self.SAMPLES)
        for chunk in (1, 2, 3, 4, 100):
            got = batched_map(self._fn, root, self.SAMPLES, chunk_size=chunk)
            self.assertEqual(baseline, got, f"chunk_size={chunk} 改变了结果")

    def test_sample_stream_depends_only_on_id(self):
        root = root_key(5)
        # 同一个样本 ID 出现在不同批次/位置，随机流一致
        r1 = batched_map(self._fn, root, [(42, 0.0)])[42]
        r2 = batched_map(self._fn, root, [(1, 0.0), (42, 0.0), (2, 0.0)])[42]
        self.assertEqual(r1, r2)
        self.assertEqual(sample_key(root, 42), sample_key(root, 42))


class TestGraphResume(unittest.TestCase):
    def _build_graph(self, strict=False):
        graph = Graph(root_key(31337), strict=strict)
        graph.add_node("x", lambda k: uniform_tensor(k, (4,)))
        graph.add_node("noise", lambda k: normal_tensor(k, (4,)))
        graph.add_node("y", lambda k, x, n: x + n, inputs=("x", "noise"))
        graph.add_node(
            "z",
            lambda k, y: dropout_forward(y, k, p=0.25)[0],
            inputs=("y",),
        )
        return graph

    def test_resume_does_not_reconsume_keys(self):
        graph = self._build_graph()
        ex = Executor(graph)
        partial = ex.run(["z"], max_nodes=2)  # 模拟中断：只完成 2 个节点
        self.assertIsNone(partial["z"])
        done_after_partial = {
            name: n.compute_count for name, n in graph.nodes.items()
        }
        self.assertEqual(sum(done_after_partial.values()), 2)

        final = ex.run(["z"])  # 断点续跑
        for name, node in graph.nodes.items():
            self.assertEqual(node.compute_count, 1, f"节点 {name} 被重复消耗键")

        # 与一次性完整执行结果一致
        graph2 = self._build_graph()
        expected = Executor(graph2).run(["z"])["z"]
        self.assertEqual(final["z"], expected)

    def test_chunk_size_does_not_change_results(self):
        results = []
        for chunk in (None, 1, 2):
            graph = self._build_graph()
            out = Executor(graph).run(["z"], chunk_size=chunk)
            results.append(out["z"])
        self.assertEqual(results[0], results[1])
        self.assertEqual(results[1], results[2])

    def test_strict_key_conflict_detected(self):
        graph = Graph(root_key(1), strict=True)
        shared = root_key(777)
        graph.add_node("a", lambda k: uniform_tensor(k, (2,)), key=shared)
        with self.assertRaises(KeyConflictError):
            graph.add_node("b", lambda k: uniform_tensor(k, (2,)), key=shared)
        # 非严格模式允许（仅记录首个占用者）
        lax = Graph(root_key(1), strict=False)
        lax.add_node("a", lambda k: uniform_tensor(k, (2,)), key=shared)
        lax.add_node("b", lambda k: uniform_tensor(k, (2,)), key=shared)

    def test_duplicate_node_name_rejected(self):
        graph = self._build_graph()
        with self.assertRaises(ValueError):
            graph.add_node("x", lambda k: None)


class TestEdgeCases(unittest.TestCase):
    def test_empty_tensor(self):
        key = root_key(3)
        empty = normal_tensor(key, (0,))
        self.assertEqual(empty.numel, 0)
        self.assertEqual(empty.shape, (0,))
        out, mask = dropout_forward(empty, key, p=0.5)
        self.assertEqual(out.numel, 0)
        self.assertEqual(mask.numel, 0)
        self.assertEqual(empty + empty, empty)
        self.assertEqual(empty.sum(), 0.0)
        self.assertEqual(batched_map(lambda k, v: v, key, []), {})
        with self.assertRaises(ValueError):
            empty.mean()

    def test_invalid_sampling_params(self):
        key = root_key(0)
        x = Tensor.full((2,), 1.0)
        with self.assertRaises(ValueError):
            bernoulli_tensor(key, (2,), p=-0.1)
        with self.assertRaises(ValueError):
            bernoulli_tensor(key, (2,), p=1.5)
        with self.assertRaises(ValueError):
            dropout_forward(x, key, p=1.0)
        with self.assertRaises(ValueError):
            dropout_forward(x, key, p=-0.5)
        with self.assertRaises(ValueError):
            fold_in(key, -1)
        with self.assertRaises(ValueError):
            root_key(-1)
        with self.assertRaises(ValueError):
            split(key, 0)
        with self.assertRaises(ValueError):
            gaussian_reparam_sample(x, Tensor.full((3,), 0.0), key)


class TestNumericalVerification(unittest.TestCase):
    """独立数值验证：矩、卡方拟合、有限差分梯度。"""

    def test_normal_moments(self):
        n = 20000
        z = normal_tensor(root_key(123), (n,))
        mean = z.mean()
        var = sum((v - mean) ** 2 for v in z.data) / n
        # 均值标准误 ≈ 1/sqrt(n) ≈ 0.0071，取 4σ 容差
        self.assertAlmostEqual(mean, 0.0, delta=0.03)
        self.assertAlmostEqual(var, 1.0, delta=0.06)

    def test_uniform_chi_square(self):
        n, bins = 10000, 10
        u = uniform_tensor(root_key(55), (n,))
        self.assertTrue(all(0.0 <= v < 1.0 for v in u.data))
        counts = [0] * bins
        for v in u.data:
            counts[int(v * bins)] += 1
        expected = n / bins
        chi2 = sum((c - expected) ** 2 / expected for c in counts)
        # 自由度 9，0.001 显著水平临界值 27.88
        self.assertLess(chi2, 27.88)

    def test_bernoulli_frequency(self):
        n, p = 20000, 0.3
        b = bernoulli_tensor(root_key(77), (n,), p)
        freq = b.mean()
        # 标准误 ≈ sqrt(p(1-p)/n) ≈ 0.0032，取 5σ
        self.assertAlmostEqual(freq, p, delta=0.02)

    def test_reparam_gradient_finite_difference(self):
        mu = Tensor([0.5, -1.0, 2.0], (3,))
        log_sigma = Tensor([0.0, -0.5, 0.3], (3,))
        key = root_key(8)

        def loss(m, ls):
            z, _ = gaussian_reparam_sample(m, ls, key)
            return (z * z).sum()

        z, eps = gaussian_reparam_sample(mu, log_sigma, key)
        grad_z = z * 2.0  # d sum(z^2)/dz
        grad_mu, grad_ls = gaussian_reparam_backward(grad_z, eps, log_sigma)

        h = 1e-6
        for i in range(3):
            mu_p = mu.to_list(); mu_p[i] += h
            mu_m = mu.to_list(); mu_m[i] -= h
            fd_mu = (loss(Tensor(mu_p, (3,)), log_sigma)
                     - loss(Tensor(mu_m, (3,)), log_sigma)) / (2 * h)
            self.assertAlmostEqual(grad_mu.data[i], fd_mu, delta=1e-4)

            ls_p = log_sigma.to_list(); ls_p[i] += h
            ls_m = log_sigma.to_list(); ls_m[i] -= h
            fd_ls = (loss(mu, Tensor(ls_p, (3,)))
                     - loss(mu, Tensor(ls_m, (3,)))) / (2 * h)
            self.assertAlmostEqual(grad_ls.data[i], fd_ls, delta=1e-4)


if __name__ == "__main__":
    unittest.main(verbosity=2)
