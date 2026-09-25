"""Runnable walkthrough of the mini-autograd engine.

Run with::

    python3.11 demo.py
"""

from mini_autograd import (
    Graph,
    Tensor,
    add,
    checkpoint,
    exp,
    faulty,
    faulty_backward,
    gradcheck,
    log,
    matmul,
    mean,
    mul,
    relu,
    rng_key,
    sub,
    sum,
    tensor,
)
from mini_autograd.errors import (
    BackwardAbortedError,
    GraphReleasedError,
    InjectedBackwardError,
    VersionMismatchError,
)


def section(title):
    print("\n" + "=" * 68)
    print(title)
    print("=" * 68)


def demo_topology_and_shared_node():
    section("1. Reverse-topological accumulation; a shared node runs once")
    with Graph().use():
        x = tensor([1.0, 2.0, 3.0], requires_grad=True)
        h = relu(x)                       # one node consumed by two branches
        loss = add(sum(h), mul(sum(h), tensor(2.0)))  # 3 * sum(h)
        relu_node = h.grad_fn
        loss.backward(retain_graph=True)
    print("loss =", loss.item(), " grad =", x.grad.data)
    print("relu node executed", relu_node.run_count, "time(s) despite 2 consumers")


def demo_version_counting():
    section("2. In-place version counting detects a stale saved input")
    with Graph().use():
        x = tensor([1.0, 2.0], requires_grad=True)
        y = sum(mul(x, x))
        print("version before mutation:", x.version)
        x.add_([0.5, -0.5])               # mutate after forward, before backward
        print("version after mutation: ", x.version)
        try:
            y.backward()
        except VersionMismatchError as exc:
            print("backward rejected:", exc)
        print("x.grad stays untouched:", x.grad)


def demo_retain_release():
    section("3. Retained vs released graphs")
    with Graph().use():
        x = tensor([1.0, 2.0], requires_grad=True)
        y = sum(mul(x, x))
        y.backward(retain_graph=True)
        print("grad after backward #1:", x.grad.data)
        y.backward()
        print("grad after backward #2 (accumulates):", x.grad.data)
        try:
            y.backward()
        except GraphReleasedError as exc:
            print("backward #3 rejected:", exc)


def demo_atomic_failure_and_independent_graphs():
    section("4. Atomic transaction; injected failure cannot poison gradients")
    with Graph("healthy").use():
        p = tensor([1.0, 1.0], requires_grad=True)
        healthy = sum(mul(p, p))
    with Graph("broken").use():
        broken = sum(faulty(p, token="boom"))
    healthy.backward()
    print("committed grad from healthy graph:", p.grad.data)
    with faulty_backward("boom"):
        try:
            broken.backward()
        except InjectedBackwardError as exc:
            print("broken graph raised:", exc)
    print("grad after failed transaction:", p.grad.data, "(unchanged)")

    # Cancellation is atomic too.
    with Graph().use():
        z = tensor([1.0, 2.0], requires_grad=True)
        q = sum(relu(z))
        calls = {"n": 0}

        def cancel(node):
            calls["n"] += 1
            return calls["n"] == 1

        try:
            q.backward(cancel=cancel)
        except BackwardAbortedError as exc:
            print("cancel raised:", exc)
        print("grad after cancel:", z.grad)


def demo_checkpoint_recompute():
    section("5. Checkpoint: bind input versions + random key, replay in backward")

    def stochastic_segment(x):
        h = relu(x)
        with rng_key(2026):
            d = __import__("mini_autograd").dropout(h, 0.4)
        return sum(d)

    with Graph().use():
        x = tensor([0.3, -0.2, 0.8, 0.5, -0.1], requires_grad=True)
        y = checkpoint(stochastic_segment, x, seed=2026)
        y.backward(retain_graph=True)
    print("checkpoint+dropout grad:", x.grad.data)

    # Numerical verification of a deterministic checkpointed segment.
    leaf_a = tensor([0.6, -0.4, 0.9], requires_grad=True)

    def build():
        return checkpoint(_seg, leaf_a, seed=7)

    err = gradcheck(build, [leaf_a], tol=1e-6)
    print(f"central-difference gradcheck relative error: {err:.3e}")


def _seg(a):
    return sum(mul(relu(a), sub(a, tensor(0.1))))


def demo_storage_lifecycle():
    section("6. Reference-counted storage survives while any graph needs it")
    import gc

    with Graph("g1").use():
        w = tensor([2.0, -1.0, 0.5], requires_grad=True)
        l1 = mean(relu(w))
    with Graph("g2").use():
        l2 = sum(mul(w, w))
    alias = w.share_storage()
    print("storage refs held by tensor + alias (graphs hold the tensors):",
          w.storage.refcount)
    l1.backward()
    print("g1 released; storage alive?", w.storage.alive, " data:", w.data)
    l2.backward()
    print("grad after g2:", w.grad.data)
    sid = w.storage.id
    del w
    gc.collect()
    print("tensor gone; alias still reads storage", sid, ":", alias.data)
    del alias
    gc.collect()
    print("alias gone -> buffer recycled (no exception in demo teardown)")


def demo_numeric_matrix():
    section("7. Independent finite differences over a matmul network")
    A = tensor([[0.4, -0.9], [1.1, 0.2]], shape=(2, 2), requires_grad=True)
    B = tensor([[0.7, 0.3], [-0.2, 0.8]], shape=(2, 2), requires_grad=True)

    def build():
        return add(
            sum(matmul(A, B)),
            sum(exp(__import__("mini_autograd").mul(A, A))),
        )

    err = gradcheck(build, [A, B], tol=1e-5, verbose=True)
    print(f"matmul+exp relative error: {err:.3e}")


if __name__ == "__main__":
    demo_topology_and_shared_node()
    demo_version_counting()
    demo_retain_release()
    demo_atomic_failure_and_independent_graphs()
    demo_checkpoint_recompute()
    demo_storage_lifecycle()
    demo_numeric_matrix()
    print("\nAll demos completed.")
