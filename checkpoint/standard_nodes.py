"""Ready-made node factories used by the demo and the tests."""
from __future__ import annotations

from functools import partial

from .graph import Node
from .rng import Rng
from .tensor import (Tensor, add, add_noise, matmul, matmul_backward,
                     sum_all, sum_all_backward, tanh, tanh_backward)


def layer_fwd(rng, x, w):
    return tanh(matmul(x, w))


def layer_bwd(g, inputs, out):
    x, w = inputs
    return matmul_backward(tanh_backward(g, out)[0], x, w)


def noise_fwd(rng, x, scale=0.1):
    return add_noise(rng, x, scale)


def noise_bwd(g, inputs, out):
    return [g.clone()]


def loss_fwd(rng, y):
    return sum_all(y)


def loss_bwd(g, inputs, out):
    return sum_all_backward(g, inputs[0])


def add_fwd(rng, a, b):
    return add(a, b)


def add_bwd(g, inputs, out):
    return [g.clone(), g.clone()]


def build_chain(dims, noise=0.1, layer_cost=5.0, noise_cost=1.0,
                loss_cost=1.0, seed=0):
    """Chain x -> (tanh(x@W) [-> +noise])* -> sum.

    Returns (nodes, input_names, inputs).
    """
    rng = Rng(seed)
    input_names = ["x"]
    inputs = {"x": Tensor.randn((1, dims[0]), rng, scale=0.5)}
    nodes = []
    prev = "x"
    for i in range(1, len(dims)):
        w = f"W{i}"
        input_names.append(w)
        inputs[w] = Tensor.randn((dims[i - 1], dims[i]), rng, scale=0.3)
        li = f"L{i}"
        nodes.append(Node(li, [prev, w], layer_fwd, layer_bwd,
                          cost=layer_cost))
        prev = li
        if noise:
            ni = f"N{i}"
            nodes.append(Node(ni, [prev], partial(noise_fwd, scale=noise),
                              noise_bwd, cost=noise_cost))
            prev = ni
    nodes.append(Node("loss", [prev], loss_fwd, loss_bwd, cost=loss_cost))
    return nodes, input_names, inputs
