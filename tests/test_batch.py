import json
import unittest

from minivmap import tensor as T
from minivmap.batch import BatchTransformError, batch_transform
from minivmap.graph import GraphBuilder
from minivmap.verify import compare_batched_vs_loop


def build_mlp(d=3, h=4):
    """Single-sample MLP regression loss: x:(d,), y:(1,) -> scalar."""
    g = GraphBuilder()
    x = g.input("x", (d,))
    y = g.input("y", (1,))
    w1 = g.input("w1", (d, h))
    b1 = g.input("b1", (h,))
    w2 = g.input("w2", (h, 1))
    pre = g.add(g.matmul(g.reshape(x, (1, d)), w1), g.reshape(b1, (1, h)))
    act = g.tanh(pre)
    pred = g.reshape(g.matmul(act, w2), (1,))
    diff = g.sub(pred, y)
    loss = g.reduce_sum(g.square(diff), axis=0)
    return g.build(outputs=[loss])


def build_shape_ops(d=3):
    """Exercises reshape -> matmul -> transpose -> reduce -> mul -> reduce."""
    g = GraphBuilder()
    x = g.input("x", (d,))
    m = g.input("m", (d, d))
    v = g.matmul(g.reshape(x, (1, d)), m)      # (1, d)
    vt = g.transpose(v, (1, 0))                # (d, 1)
    s = g.reduce_sum(vt, 1)                    # (d,)
    out = g.reduce_mean(g.mul(s, x), 0)        # scalar
    return g.build(outputs=[out]), vt, s


def mlp_env(batch, seed=0):
    return {
        "x": T.Tensor.randn((batch, 3), seed=seed + 1),
        "y": T.Tensor.randn((batch, 1), seed=seed + 2),
        "w1": T.Tensor.randn((3, 4), seed=seed + 3),
        "b1": T.Tensor.randn((4,), seed=seed + 4),
        "w2": T.Tensor.randn((4, 1), seed=seed + 5),
    }


IN_AXES = {"x": 0, "y": 0, "w1": None, "b1": None, "w2": None}
WRT = ["w1", "b1", "w2", "x"]


class TestBatchTransform(unittest.TestCase):
    def test_forward_and_grad_match_per_sample_loop(self):
        single = build_mlp()
        env = mlp_env(5)
        shapes = {k: v.shape for k, v in env.items()}
        batched, cert = batch_transform(single, IN_AXES, shapes)
        self.assertEqual(cert.status, "ok")
        self.assertEqual(cert.batch_size, 5)
        report = compare_batched_vs_loop(single, IN_AXES, env, WRT, batched)
        self.assertTrue(report["ok"], report)

    def test_axis_tracking_through_transpose_reduce_reshape(self):
        single, vt, s = build_shape_ops()
        env = {"x": T.Tensor.randn((7, 3), seed=1), "m": T.Tensor.randn((3, 3), seed=2)}
        shapes = {k: v.shape for k, v in env.items()}
        batched, cert = batch_transform(single, {"x": 0, "m": None}, shapes)
        # mapped axis stays the leading axis through reshape/matmul/transpose/reduce
        self.assertEqual(cert.node_axes[vt]["batch_dims"], [0])
        self.assertEqual(cert.node_axes[vt]["shape"], [7, 3, 1])
        self.assertEqual(cert.node_axes[s]["batch_dims"], [0])
        self.assertEqual(cert.node_axes[s]["shape"], [7, 3])
        self.assertEqual(cert.outputs[single.outputs[0]]["batch_dims"], [0])
        self.assertEqual(cert.outputs[single.outputs[0]]["shape"], [7])
        report = compare_batched_vs_loop(single, {"x": 0, "m": None}, env,
                                         ["m", "x"], batched)
        self.assertTrue(report["ok"], report)

    def test_mixed_mapped_axes_are_rearranged(self):
        single = build_mlp()
        env = mlp_env(5)
        env["x"] = T.moveaxis(env["x"], 0, 1)  # store x features-first: (3, 5)
        in_axes = dict(IN_AXES, **{"x": 1})
        shapes = {k: v.shape for k, v in env.items()}
        batched, cert = batch_transform(single, in_axes, shapes)
        self.assertEqual(cert.axis_moves,
                         [{"input": "x", "from_axis": 1, "to_axis": 0,
                           "node": "x__batch_front"}])
        report = compare_batched_vs_loop(single, in_axes, env, WRT, batched)
        self.assertTrue(report["ok"], report)

    def test_nested_batch_mapping(self):
        single = build_mlp()
        env1 = mlp_env(5)
        shapes1 = {k: v.shape for k, v in env1.items()}
        level1, _ = batch_transform(single, IN_AXES, shapes1)
        # second mapping level: x (2, 5, 3), y (2, 5, 1)
        env2 = dict(env1)
        env2["x"] = T.Tensor.randn((2, 5, 3), seed=11)
        env2["y"] = T.Tensor.randn((2, 5, 1), seed=12)
        shapes2 = {k: v.shape for k, v in env2.items()}
        level2, cert2 = batch_transform(level1, {"x": 0, "y": 0}, shapes2)
        self.assertEqual(cert2.batch_size, 2)
        out = single.outputs[0]
        self.assertEqual(cert2.outputs[out]["batch_dims"], [0])
        self.assertEqual(cert2.outputs[out]["shape"], [2, 5])
        # verify level-2 against per-sample loop of the level-1 graph
        report = compare_batched_vs_loop(level1, {"x": 0, "y": 0}, env2,
                                         WRT, level2)
        self.assertTrue(report["ok"], report)

    def test_batch_size_mismatch_rejected_early(self):
        single = build_mlp()
        env = mlp_env(5)
        env["y"] = T.Tensor.randn((4, 1), seed=9)  # 4 != 5 samples
        shapes = {k: v.shape for k, v in env.items()}
        with self.assertRaises(BatchTransformError) as ctx:
            batch_transform(single, IN_AXES, shapes)
        cert = ctx.exception.certificate
        self.assertEqual(cert.status, "rejected")
        self.assertIn("batch size mismatch", str(ctx.exception))

    def test_side_effect_op_rejected_with_dependency_path(self):
        g = GraphBuilder()
        x = g.input("x", (3,))
        w = g.input("w", (3,))
        h = g.mul(x, w)
        e = g.emit("hidden", h)
        loss = g.reduce_sum(e, axis=0)
        single = g.build(outputs=[loss])
        shapes = {"x": (4, 3), "w": (3,)}
        with self.assertRaises(BatchTransformError) as ctx:
            batch_transform(single, {"x": 0, "w": None}, shapes)
        cert = ctx.exception.certificate
        self.assertEqual(cert.status, "rejected")
        self.assertEqual(len(cert.rejected), 1)
        rej = cert.rejected[0]
        self.assertEqual(rej["node"], e)
        # minimal dependency path: mapped input -> mul -> emit
        self.assertEqual(rej["dependency_path"], ["x", h, e])

    def test_cross_sample_collective_rejected(self):
        g = GraphBuilder()
        x = g.input("x", (3,))
        w = g.input("w", (3,))
        h = g.mul(x, w)
        c = g.cross_sample_sum(h)
        loss = g.reduce_sum(c, axis=0)
        single = g.build(outputs=[loss])
        shapes = {"x": (4, 3), "w": (3,)}
        with self.assertRaises(BatchTransformError) as ctx:
            batch_transform(single, {"x": 0, "w": None}, shapes)
        rej = ctx.exception.certificate.rejected[0]
        self.assertEqual(rej["op"], "cross_sample_sum")
        self.assertEqual(rej["dependency_path"][0], "x")
        self.assertEqual(rej["dependency_path"][-1], c)

    def test_certificate_is_json_serialisable(self):
        single = build_mlp()
        env = mlp_env(3)
        shapes = {k: v.shape for k, v in env.items()}
        _, cert = batch_transform(single, IN_AXES, shapes)
        parsed = json.loads(cert.to_json())
        self.assertEqual(parsed["batch_size"], 3)
        self.assertEqual(parsed["shared_inputs"], ["w1", "b1", "w2"])
        self.assertEqual(parsed["mapped_inputs"], ["x", "y"])


if __name__ == "__main__":
    unittest.main()
