"""End-to-end demo of minigrad: training step, retain/release, atomic
transactions, recompute nodes, and storage lifecycle.  Run with:

    python3.11 examples/demo.py
"""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from minigrad import (  # noqa: E402
    CancelToken, BackwardCancelled, Graph, GraphReleasedError,
    StaleInputError, Tensor, ops,
)


def main():
    print("== 1. forward + reverse-topological backward ==")
    with Graph("train") as g:
        x = Tensor.rand((2, 3), seed=1, requires_grad=True, name="x")
        w = Tensor.rand((3, 2), seed=2, requires_grad=True, name="w")
        b = Tensor.rand((2,), seed=3, requires_grad=True, name="b")
        loss = ((x @ w + b).tanh() * (x @ w + b)).mean()
        g.backward(loss, retain_graph=True)
    print(f"loss={loss.item():.6f}  dW[0]={w.grad.data[0]:.6f}")

    print("== 2. retained graph: second backward accumulates ==")
    g.backward(loss)  # consumes the graph
    print(f"dW[0] doubled -> {w.grad.data[0]:.6f}")
    try:
        g.backward(loss)
    except GraphReleasedError as e:
        print(f"third backward refused: {e}")

    print("== 3. in-place mutation of a saved input is caught ==")
    with Graph("stale") as g2:
        v = Tensor([2.0, 3.0], requires_grad=True, name="v")
        y = (v * v).sum()
        v.add_(1.0)
        try:
            g2.backward(y)
        except StaleInputError as e:
            print(f"stale input detected: {e}")

    print("== 4. atomic transaction: failure commits nothing ==")
    p = Tensor([1.0, -2.0], requires_grad=True, name="p")
    with Graph("ok") as g3:
        good = (p * p).sum()
        g3.backward(good)
    committed = list(p.grad.data)
    g4 = Graph("boom")
    with g4:
        a = p * 2.0

        def fwd(t):
            return [v * 3.0 for v in t.data], t.shape

        def bwd(gout, node):
            raise RuntimeError("injected mid-graph failure")

        bad = ops.apply((a,), fwd, bwd, name="boom").sum()
    try:
        g4.backward(bad)
    except RuntimeError as e:
        print(f"rollback after: {e}; p.grad still {p.grad.data} == {committed}")

    print("== 5. cancellation rolls back too ==")
    with Graph("cancel") as g5:
        y5 = (p * p * p).sum()
        token = CancelToken()
        token.cancel()
        try:
            g5.backward(y5, cancel_token=token)
        except BackwardCancelled as e:
            print(f"cancelled: {e}; p.grad unchanged: {p.grad.data == committed}")

    print("== 6. recompute node (dropout) bound to version + random key ==")
    import random
    d = Tensor.rand((4,), seed=9, requires_grad=True, name="d")
    with Graph("drop") as g6:
        h = ops.dropout(d, p=0.5, key=42).sum()
        g6.backward(h, retain_graph=True)
        once = list(d.grad.data)
        g6.backward(h)
    rng = random.Random(42)
    mask = [2.0 if rng.random() >= 0.5 else 0.0 for _ in range(4)]
    print(f"recomputed mask matches eager mask: {once == mask}")
    print(f"retained second backward accumulates: "
          f"{d.grad.data == [2 * m for m in mask]}")

    print("== 7. storage pinned by another graph survives release ==")
    shared = Tensor([1.5, -0.5], requires_grad=True, name="shared")
    with Graph("A") as gA:
        _ = (shared * 2.0).sum()
    with Graph("B") as gB:
        yB = (shared * shared).sum()
    gA.release()
    print(f"after releasing A: storage freed={shared._storage.is_freed}, "
          f"pins={shared._storage.pins}")
    gB.backward(yB)
    print(f"B backward still works: d(shared)={shared.grad.data}")


if __name__ == "__main__":
    main()
