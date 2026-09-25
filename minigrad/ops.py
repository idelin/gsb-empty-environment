"""Differentiable ops.  Every op records a Node into the current graph."""

import math
import random

from .graph import Node, current_graph
from .tensor import Tensor, _prod, _strides


# -- broadcasting helpers -------------------------------------------------

def broadcast_shape(s1, s2):
    out = []
    for a, b in zip(
            [1] * (len(s2) - len(s1)) + list(s1) if len(s2) > len(s1) else s1,
            [1] * (len(s1) - len(s2)) + list(s2) if len(s1) > len(s2) else s2):
        if a != b and a != 1 and b != 1:
            raise ValueError(f"cannot broadcast {s1} and {s2}")
        out.append(max(a, b))
    return tuple(out)


def _bstrides(in_shape, out_shape):
    pad = len(out_shape) - len(in_shape)
    st = _strides(in_shape)
    return (0,) * pad + tuple(0 if d == 1 else st[i - pad]
                              for i, d in enumerate(in_shape))


def _unravel(flat, shape):
    idx = []
    for st, d in zip(_strides(shape), shape):
        idx.append((flat // st) % d)
    return idx


def broadcast_data(t, out_shape):
    if t.shape == out_shape:
        return list(t.data)
    bst = _bstrides(t.shape, out_shape)
    out = []
    for flat in range(_prod(out_shape)):
        idx = _unravel(flat, out_shape)
        src = sum(i * s for i, s in zip(idx, bst))
        out.append(t.data[src])
    return out


def unbroadcast(grad, out_shape, in_shape):
    """Sum a grad of out_shape back down to in_shape."""
    if out_shape == in_shape:
        return list(grad)
    out = [0.0] * _prod(in_shape)
    bst = _bstrides(in_shape, out_shape)
    for flat in range(_prod(out_shape)):
        idx = _unravel(flat, out_shape)
        dst = sum(i * s for i, s in zip(idx, bst))
        out[dst] += grad[flat]
    return out


def _as_tensor(x):
    return x if isinstance(x, Tensor) else Tensor.scalar(x)


def _record(name, inputs, output, backward_fn, saved=(), recompute=None,
            rng_key=None):
    graph = current_graph()
    if graph is None:
        return output  # graph-free evaluation (e.g. numerical checks)
    node = Node(graph, name, inputs, output, backward_fn,
                saved=saved, recompute=recompute, rng_key=rng_key)
    graph.add_node(node)
    output._node = node
    return output


# -- elementwise binary ----------------------------------------------------

def _binary(a, b, fwd, ga, gb, name):
    a, b = _as_tensor(a), _as_tensor(b)
    shape = broadcast_shape(a.shape, b.shape)
    da, db = broadcast_data(a, shape), broadcast_data(b, shape)
    out = Tensor([fwd(x, y) for x, y in zip(da, db)], shape=shape,
                 requires_grad=a.requires_grad or b.requires_grad)

    def backward(gout, node):
        node_saved = node.saved
        sa, sb = node_saved[0], node_saved[1]
        ba = broadcast_data(sa, shape)
        bb = broadcast_data(sb, shape)
        grads = []
        if a.requires_grad:
            grads.append((a, unbroadcast(
                [ga(g, x, y) for g, x, y in zip(gout, ba, bb)], shape, a.shape)))
        if b.requires_grad:
            grads.append((b, unbroadcast(
                [gb(g, x, y) for g, x, y in zip(gout, ba, bb)], shape, b.shape)))
        return grads

    if out.requires_grad:
        _record(name, (a, b), out, backward, saved=(a, b))
    return out


def add(a, b):
    return _binary(a, b, lambda x, y: x + y,
                   lambda g, x, y: g, lambda g, x, y: g, "add")


def sub(a, b):
    return _binary(a, b, lambda x, y: x - y,
                   lambda g, x, y: g, lambda g, x, y: -g, "sub")


def mul(a, b):
    return _binary(a, b, lambda x, y: x * y,
                   lambda g, x, y: g * y, lambda g, x, y: g * x, "mul")


def div(a, b):
    return _binary(a, b, lambda x, y: x / y,
                   lambda g, x, y: g / y,
                   lambda g, x, y: -g * x / (y * y), "div")


def neg(a):
    return mul(a, -1.0)


# -- unary -----------------------------------------------------------------

def _unary(a, fwd, bwd, name):
    out = Tensor([fwd(x) for x in a.data], shape=a.shape,
                 requires_grad=a.requires_grad)

    def backward(gout, node):
        s = node.saved[0]
        o = node.saved[1]
        return [(a, [bwd(g, x, y) for g, x, y in zip(gout, s.data, o.data)])]

    if out.requires_grad:
        _record(name, (a,), out, backward, saved=(a, out))
    return out


def exp(a):
    return _unary(a, math.exp, lambda g, x, y: g * y, "exp")


def log(a):
    return _unary(a, math.log, lambda g, x, y: g / x, "log")


def tanh(a):
    return _unary(a, math.tanh, lambda g, x, y: g * (1 - y * y), "tanh")


def relu(a):
    return _unary(a, lambda x: x if x > 0 else 0.0,
                  lambda g, x, y: g if x > 0 else 0.0, "relu")


def sigmoid(a):
    return _unary(a, lambda x: 1.0 / (1.0 + math.exp(-x)),
                  lambda g, x, y: g * y * (1 - y), "sigmoid")


# -- reductions --------------------------------------------------------------

def sum_all(a):
    out = Tensor.scalar(sum(a.data), requires_grad=a.requires_grad)

    def backward(gout, node):
        return [(a, [gout[0]] * a.size)]

    if out.requires_grad:
        _record("sum", (a,), out, backward, saved=(a,))
    return out


def mean_all(a):
    out = Tensor.scalar(sum(a.data) / a.size, requires_grad=a.requires_grad)

    def backward(gout, node):
        return [(a, [gout[0] / a.size] * a.size)]

    if out.requires_grad:
        _record("mean", (a,), out, backward, saved=(a,))
    return out


# -- matmul (2-D) ------------------------------------------------------------

def matmul(a, b):
    if a.ndim != 2 or b.ndim != 2 or a.shape[1] != b.shape[0]:
        raise ValueError("matmul expects (m,k) @ (k,n)")
    m, k, n = a.shape[0], a.shape[1], b.shape[1]
    out_data = [0.0] * (m * n)
    for i in range(m):
        for j in range(n):
            s = 0.0
            for p in range(k):
                s += a.data[i * k + p] * b.data[p * n + j]
            out_data[i * n + j] = s
    out = Tensor(out_data, shape=(m, n),
                 requires_grad=a.requires_grad or b.requires_grad)

    def backward(gout, node):
        sa, sb = node.saved
        grads = []
        if a.requires_grad:  # g @ b^T
            ga = [0.0] * (m * k)
            for i in range(m):
                for p in range(k):
                    s = 0.0
                    for j in range(n):
                        s += gout[i * n + j] * sb.data[p * n + j]
                    ga[i * k + p] = s
            grads.append((a, ga))
        if b.requires_grad:  # a^T @ g
            gb = [0.0] * (k * n)
            for p in range(k):
                for j in range(n):
                    s = 0.0
                    for i in range(m):
                        s += sa.data[i * k + p] * gout[i * n + j]
                    gb[p * n + j] = s
            grads.append((b, gb))
        return grads

    if out.requires_grad:
        _record("matmul", (a, b), out, backward, saved=(a, b))
    return out


# -- recompute node: dropout with a bound random key -------------------------

def dropout(a, p, key):
    """Inverted dropout.  The mask is NOT saved; backward recomputes it
    deterministically from the bound random ``key`` after validating the
    input's version, so memory stays low and mutation is still caught."""
    if not 0.0 <= p < 1.0:
        raise ValueError("p must be in [0, 1)")
    scale = 1.0 / (1.0 - p)

    def make_mask():
        rng = random.Random(key)
        return [(scale if rng.random() >= p else 0.0) for _ in range(a.size)]

    mask = make_mask()
    out = Tensor([x * m for x, m in zip(a.data, mask)], shape=a.shape,
                 requires_grad=a.requires_grad)

    def backward(gout, node):
        recomputed = make_mask()  # bound to rng_key, deterministic
        return [(a, [g * m for g, m in zip(gout, recomputed)])]

    if out.requires_grad:
        _record("dropout", (a,), out, backward, saved=(a,), rng_key=key)
    return out


# -- escape hatch for custom ops (used to inject failures in tests) ---------

def apply(inputs, forward, backward, name="custom", saved=()):
    inputs = tuple(_as_tensor(t) for t in inputs)
    out_data, out_shape = forward(*inputs)
    out = Tensor(out_data, shape=out_shape,
                 requires_grad=any(t.requires_grad for t in inputs))
    if out.requires_grad:
        _record(name, inputs, out, backward, saved=saved or inputs)
    return out
