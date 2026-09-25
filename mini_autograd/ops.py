"""Differentiable CPU operators.

Each op computes its output with a kernel from :mod:`mini_autograd.kernels` and,
when autograd is enabled, attaches a :class:`~mini_autograd.graph.Node` whose
backward rule returns one gradient per *requires_grad* input, in order.
"""

from __future__ import annotations

import threading

from . import kernels as K
from .errors import InjectedBackwardError
from .graph import Node, current_graph, is_grad_enabled
from .rng import random_unit
from .tensor import Tensor


# ---------------------------------------------------------------------
# construction helpers
# ---------------------------------------------------------------------
def tensor(data, shape=None, requires_grad=False):
    return Tensor(data, shape=shape, requires_grad=requires_grad)


def zeros(n, requires_grad=False):
    return Tensor([0.0] * n, (n,), requires_grad=requires_grad)


def ones(n, requires_grad=False):
    return Tensor([1.0] * n, (n,), requires_grad=requires_grad)


def _as_tensor(x):
    return x if isinstance(x, Tensor) else Tensor(float(x), ())


def _make(name, inputs, data, shape, backward_fn, saved=None, metadata=None):
    graph = current_graph(strict=True)
    out = Tensor(data, shape, requires_grad=True)
    node = Node(
        name,
        graph,
        inputs=tuple(t for t in inputs if t.requires_grad),
        outputs=(out,),
        backward_fn=backward_fn,
        saved=saved,
        metadata=metadata,
    )
    out.grad_fn = node
    out.graph = graph
    graph.add_node(node)
    return out


def _want_graph(inputs):
    return is_grad_enabled() and any(t.requires_grad for t in inputs)


# ---------------------------------------------------------------------
# elementwise binary
# ---------------------------------------------------------------------
def add(a, b):
    a, b = _as_tensor(a), _as_tensor(b)
    shape = K.broadcast_shape(a.shape, b.shape)
    data = K.k_add(a.data, b.data)
    if not _want_graph((a, b)):
        return Tensor(data, shape)

    def bwd(out_grads, node):
        (g,) = out_grads
        grads = []
        if a.requires_grad:
            grads.append(Tensor(K.unbroadcast(g, shape, a.shape), a.shape))
        if b.requires_grad:
            grads.append(Tensor(K.unbroadcast(g, shape, b.shape), b.shape))
        return grads

    return _make("add", (a, b), data, shape, bwd)


def sub(a, b):
    a, b = _as_tensor(a), _as_tensor(b)
    shape = K.broadcast_shape(a.shape, b.shape)
    data = K.k_sub(a.data, b.data)
    if not _want_graph((a, b)):
        return Tensor(data, shape)

    def bwd(out_grads, node):
        (g,) = out_grads
        grads = []
        if a.requires_grad:
            grads.append(Tensor(K.unbroadcast(g, shape, a.shape), a.shape))
        if b.requires_grad:
            grads.append(Tensor(K.k_neg(K.unbroadcast(g, shape, b.shape)), b.shape))
        return grads

    return _make("sub", (a, b), data, shape, bwd)


def mul(a, b):
    a, b = _as_tensor(a), _as_tensor(b)
    shape = K.broadcast_shape(a.shape, b.shape)
    data = K.k_mul(a.data, b.data)
    if not _want_graph((a, b)):
        return Tensor(data, shape)

    def bwd(out_grads, node):
        (g,) = out_grads
        grads = []
        if a.requires_grad:
            ga = K.k_mul(g, b.data)
            grads.append(Tensor(K.unbroadcast(ga, shape, a.shape), a.shape))
        if b.requires_grad:
            gb = K.k_mul(g, a.data)
            grads.append(Tensor(K.unbroadcast(gb, shape, b.shape), b.shape))
        return grads

    return _make("mul", (a, b), data, shape, bwd, saved=(a, b))


def div(a, b):
    a, b = _as_tensor(a), _as_tensor(b)
    shape = K.broadcast_shape(a.shape, b.shape)
    data = K.k_div(a.data, b.data)
    if not _want_graph((a, b)):
        return Tensor(data, shape)

    def bwd(out_grads, node):
        (g,) = out_grads
        grads = []
        if a.requires_grad:
            ga = K.k_div(g, b.data)
            grads.append(Tensor(K.unbroadcast(ga, shape, a.shape), a.shape))
        if b.requires_grad:
            gb = K.k_neg(K.k_div(K.k_mul(g, a.data), K.k_mul(b.data, b.data)))
            grads.append(Tensor(K.unbroadcast(gb, shape, b.shape), b.shape))
        return grads

    return _make("div", (a, b), data, shape, bwd, saved=(a, b))


def neg(a):
    data = K.k_neg(a.data)
    if not _want_graph((a,)):
        return Tensor(data, a.shape)

    def bwd(out_grads, node):
        (g,) = out_grads
        return [Tensor(K.k_neg(g), a.shape)]

    return _make("neg", (a,), data, a.shape, bwd)


def pow_scalar(a, p):
    p = float(p)
    data = K.k_pow_scalar(a.data, p)
    if not _want_graph((a,)):
        return Tensor(data, a.shape)

    def bwd(out_grads, node):
        (g,) = out_grads
        coef = node.metadata["p"]
        factor = K.k_pow_scalar(a.data, coef - 1.0)
        return [Tensor([coef * f * gi for f, gi in zip(factor, g)], a.shape)]

    return _make("pow", (a,), data, a.shape, bwd, saved=(a,), metadata={"p": p})


# ---------------------------------------------------------------------
# nonlinearities
# ---------------------------------------------------------------------
def relu(a):
    data = K.k_relu(a.data)
    if not _want_graph((a,)):
        return Tensor(data, a.shape)

    def bwd(out_grads, node):
        (g,) = out_grads
        saved = node.saved[0].data
        return [Tensor([gi if x > 0.0 else 0.0 for x, gi in zip(saved, g)], a.shape)]

    return _make("relu", (a,), data, a.shape, bwd, saved=(a,))


def exp(a):
    data = K.k_exp(a.data)
    if not _want_graph((a,)):
        return Tensor(data, a.shape)

    def bwd(out_grads, node):
        (g,) = out_grads
        forward = node.metadata["out_data"]
        return [Tensor(K.k_mul(g, forward), a.shape)]

    return _make(
        "exp", (a,), data, a.shape, bwd, metadata={"out_data": list(data)}
    )


def log(a):
    data = K.k_log(a.data)
    if not _want_graph((a,)):
        return Tensor(data, a.shape)

    def bwd(out_grads, node):
        (g,) = out_grads
        x = node.saved[0].data
        return [Tensor(K.k_div(g, x), a.shape)]

    return _make("log", (a,), data, a.shape, bwd, saved=(a,))


# ---------------------------------------------------------------------
# reductions and linear algebra
# ---------------------------------------------------------------------
def sum(a):
    data = K.k_sum(a.data)
    if not _want_graph((a,)):
        return Tensor(data, ())

    def bwd(out_grads, node):
        (g,) = out_grads
        s = g[0]
        return [Tensor([s] * len(a.data), a.shape)]

    return _make("sum", (a,), data, (), bwd)


def mean(a):
    n = len(a.data)
    data = K.k_mean(a.data)
    if not _want_graph((a,)):
        return Tensor(data, ())

    def bwd(out_grads, node):
        (g,) = out_grads
        s = g[0] / node.metadata["n"]
        return [Tensor([s] * node.metadata["n"], a.shape)]

    return _make("mean", (a,), data, (), bwd, metadata={"n": n})


def matmul(a, b):
    if len(a.shape) != 2 or len(b.shape) != 2 or a.shape[1] != b.shape[0]:
        raise ValueError("matmul requires 2-D compatible tensors")
    m, k = a.shape
    k2, n = b.shape
    data = K.k_matmul(a.data, b.data, m, k, n)
    shape = (m, n)
    if not _want_graph((a, b)):
        return Tensor(data, shape)

    def bwd(out_grads, node):
        (g,) = out_grads
        grads = []
        if a.requires_grad:
            bt = K.k_transpose2(b.data, k, n)
            grads.append(Tensor(K.k_matmul(g, bt, m, n, k), (m, k)))
        if b.requires_grad:
            at = K.k_transpose2(a.data, m, k)
            grads.append(Tensor(K.k_matmul(at, g, k, m, n), (k, n)))
        return grads

    return _make("matmul", (a, b), data, shape, bwd, saved=(a, b))


# ---------------------------------------------------------------------
# stochastic operator (key-seeded)
# ---------------------------------------------------------------------
def dropout(a, p=0.5):
    if not 0.0 <= p < 1.0:
        raise ValueError("p must be in [0, 1)")
    mask = [1 if random_unit() >= p else 0 for _ in a.data]
    scale = 1.0 / (1.0 - p)
    data = K.k_dropout(a.data, mask, scale)
    if not _want_graph((a,)):
        return Tensor(data, a.shape)

    def bwd(out_grads, node):
        (g,) = out_grads
        m = node.metadata["mask"]
        sc = node.metadata["scale"]
        return [Tensor([gi * bit * sc for gi, bit in zip(g, m)], a.shape)]

    return _make(
        "dropout",
        (a,),
        data,
        a.shape,
        bwd,
        metadata={"mask": mask, "scale": scale},
    )


# ---------------------------------------------------------------------
# fault-injection operator for lifecycle / atomicity tests
# ---------------------------------------------------------------------
_FAULT_TLS = threading.local()
_FAULT_LOCK = threading.Lock()


class faulty_backward:
    """Thread-local context: ``faulty`` raises after ``arm`` input grads."""

    def __init__(self, token, arm=True):
        self.token = token
        self.arm = arm

    def __enter__(self):
        self._prev = getattr(_FAULT_TLS, "armed", {})
        merged = dict(self._prev)
        merged[self.token] = self.arm
        _FAULT_TLS.armed = merged
        return self

    def __exit__(self, *exc):
        _FAULT_TLS.armed = self._prev
        return False


def _fault_armed(token):
    armed = getattr(_FAULT_TLS, "armed", {})
    with _FAULT_LOCK:
        return bool(armed.get(token, False))


def faulty(a, token="op"):
    """Identity in forward; raises :class:`InjectedBackwardError` in backward
    while the matching :class:`faulty_backward` context is armed."""
    data = list(a.data)
    if not _want_graph((a,)):
        return Tensor(data, a.shape)

    def bwd(out_grads, node):
        (g,) = out_grads
        if _fault_armed(node.metadata["token"]):
            raise InjectedBackwardError(
                f"injected failure in backward of '{node.metadata['token']}'"
            )
        return [Tensor(list(g), a.shape)]

    return _make(
        "faulty", (a,), data, a.shape, bwd, metadata={"token": token}
    )
