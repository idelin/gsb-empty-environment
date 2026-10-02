"""End-to-end demo: batch-transform a single-sample MLP loss graph.

Covers: mapped axes vs shared parameters, axis tracking certificate,
per-sample-loop + reverse-mode verification, finite-difference checks,
nested batch mapping, mixed mapped axes, and three rejection cases.

Run:  python3 examples/demo_mlp.py
"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from minivmap import tensor as T
from minivmap.batch import BatchTransformError, batch_transform
from minivmap.grad import value_and_grad
from minivmap.graph import GraphBuilder, format_graph
from minivmap.verify import (compare_batched_vs_loop, finite_difference_grad)

IN_AXES = {"x": 0, "y": 0, "w1": None, "b1": None, "w2": None}
WRT = ["w1", "b1", "w2", "x"]


def build_mlp(d=3, h=4):
    """Single-sample graph: x:(d,), y:(1,) -> scalar MSE loss."""
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


def banner(title):
    print("\n" + "=" * 68 + f"\n{title}\n" + "=" * 68)


def main():
    single = build_mlp()
    env = {
        "x": T.Tensor.randn((5, 3), seed=1),
        "y": T.Tensor.randn((5, 1), seed=2),
        "w1": T.Tensor.randn((3, 4), seed=3),
        "b1": T.Tensor.randn((4,), seed=4),
        "w2": T.Tensor.randn((4, 1), seed=5),
    }
    shapes = {k: v.shape for k, v in env.items()}

    banner("1. Single-sample graph")
    print(format_graph(single))

    banner("2. Batch transform (x, y mapped on axis 0; w1/b1/w2 shared)")
    batched, cert = batch_transform(single, IN_AXES, shapes)
    print(format_graph(batched))
    print("\nCertificate (excerpt):")
    print(f"  batch_size    = {cert.batch_size}")
    print(f"  mapped_inputs = {cert.mapped_inputs}")
    print(f"  shared_inputs = {cert.shared_inputs}")
    print(f"  outputs       = {cert.outputs}")
    for name in ("n0", "n4", "n8"):
        print(f"  axis track {name}: {cert.node_axes[name]}")

    banner("3. Verification: batched vs per-sample loop (forward AND reverse)")
    report = compare_batched_vs_loop(single, IN_AXES, env, WRT, batched)
    for key, value in report.items():
        print(f"  {key:16s} {value if isinstance(value, bool) else f'{value:.3e}'}")
    assert report["ok"]

    banner("4. Independent check: finite differences vs batched reverse mode")
    _, grads = value_and_grad(batched, ["w1", "b1", "w2"], env)
    fd = finite_difference_grad(batched, ["w1", "b1", "w2"], env)
    for w in fd:
        diff = T.max_abs_diff(grads[w], fd[w])
        print(f"  |grad_{w} - finitediff|max = {diff:.3e}")
        assert diff < 1e-4

    banner("5. Nested batch mapping: vmap(vmap(f)) with x (2, 5, 3)")
    env2 = dict(env)
    env2["x"] = T.Tensor.randn((2, 5, 3), seed=11)
    env2["y"] = T.Tensor.randn((2, 5, 1), seed=12)
    shapes2 = {k: v.shape for k, v in env2.items()}
    level2, cert2 = batch_transform(batched, {"x": 0, "y": 0}, shapes2)
    report2 = compare_batched_vs_loop(batched, {"x": 0, "y": 0}, env2, WRT, level2)
    print(f"  outer batch_size = {cert2.batch_size}")
    print(f"  output axis track = {cert2.outputs[single.outputs[0]]}")
    print(f"  forward+grad vs nested loop ok = {report2['ok']}")
    assert report2["ok"]

    banner("6. Mixed mapped axes: x stored features-first (3, 5), mapped on axis 1")
    env3 = dict(env)
    env3["x"] = T.moveaxis(env["x"], 0, 1)
    in_axes3 = dict(IN_AXES, **{"x": 1})
    shapes3 = {k: v.shape for k, v in env3.items()}
    batched3, cert3 = batch_transform(single, in_axes3, shapes3)
    report3 = compare_batched_vs_loop(single, in_axes3, env3, WRT, batched3)
    print(f"  axis_moves = {cert3.axis_moves}")
    print(f"  forward+grad vs loop ok = {report3['ok']}")
    assert report3["ok"]

    banner("7. Rejections (certificate records reason + minimal dependency path)")
    g = GraphBuilder()
    x = g.input("x", (3,))
    w = g.input("w", (3,))
    h = g.mul(x, w)
    e = g.emit("hidden", h)
    loss = g.reduce_sum(e, axis=0)
    impure = g.build(outputs=[loss])
    try:
        batch_transform(impure, {"x": 0, "w": None}, {"x": (4, 3), "w": (3,)})
        raise AssertionError("expected rejection")
    except BatchTransformError as err:
        print("  side-effect op rejected:")
        print("  " + err.certificate.to_json().replace("\n", "\n  "))

    g2 = GraphBuilder()
    x2 = g2.input("x", (3,))
    w2_ = g2.input("w", (3,))
    c = g2.cross_sample_sum(g2.mul(x2, w2_))
    loss2 = g2.reduce_sum(c, axis=0)
    collective = g2.build(outputs=[loss2])
    try:
        batch_transform(collective, {"x": 0, "w": None}, {"x": (4, 3), "w": (3,)})
        raise AssertionError("expected rejection")
    except BatchTransformError as err:
        print(f"\n  cross-sample collective rejected: {err}")
        print(f"  dependency path: {err.certificate.rejected[0]['dependency_path']}")

    bad = dict(shapes)
    bad["y"] = (4, 1)  # 4 samples vs 5 for x
    try:
        batch_transform(single, IN_AXES, bad)
        raise AssertionError("expected rejection")
    except BatchTransformError as err:
        print(f"\n  batch-size mismatch rejected early: {err}")

    banner("ALL DEMO CHECKS PASSED")


if __name__ == "__main__":
    main()
