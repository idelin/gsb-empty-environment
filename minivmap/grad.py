"""Reverse-mode gradients over pure graphs.

Works uniformly on single-sample and batched graphs: broadcasting-aware VJPs
("unbroadcast") make shared-parameter gradients sum over batch axes
automatically when the loss seed is a vector of ones.
"""
from __future__ import annotations

from . import tensor as T
from .graph import Graph, evaluate


def _vjp(node, values, g):
    """Return gradient tensors aligned with node.inputs."""
    op = node.op
    ins = [values[i] for i in node.inputs]
    if op == "add":
        return [T.unbroadcast(g, ins[0].shape), T.unbroadcast(g, ins[1].shape)]
    if op == "sub":
        return [T.unbroadcast(g, ins[0].shape),
                T.unbroadcast(T.neg(g), ins[1].shape)]
    if op == "mul":
        return [T.unbroadcast(T.mul(g, ins[1]), ins[0].shape),
                T.unbroadcast(T.mul(g, ins[0]), ins[1].shape)]
    if op == "div":
        a, b = ins
        return [T.unbroadcast(T.div(g, b), a.shape),
                T.unbroadcast(T.neg(T.div(T.mul(g, a), T.mul(b, b))), b.shape)]
    if op == "neg":
        return [T.neg(g)]
    if op == "exp":
        return [T.mul(g, values[node.name])]
    if op == "tanh":
        y = values[node.name]
        return [T.mul(g, T.sub(T.ones_like(y), T.mul(y, y)))]
    if op == "log":
        return [T.div(g, ins[0])]
    if op == "square":
        return [T.mul(g, T.add(ins[0], ins[0]))]
    if op == "matmul":
        a, b = ins
        da = T.matmul(g, T.transpose_last2(b))
        db = T.matmul(T.transpose_last2(a), g)
        return [T.unbroadcast(da, a.shape), T.unbroadcast(db, b.shape)]
    if op == "transpose":
        perm = tuple(p % len(ins[0].shape) for p in node.params["perm"])
        inv = tuple(perm.index(i) for i in range(len(perm)))
        return [T.transpose(g, inv)]
    if op == "reshape":
        return [T.reshape(g, ins[0].shape)]
    if op == "reduce_sum":
        axis = node.params["axis"] % len(ins[0].shape)
        kept = ins[0].shape[:axis] + (1,) + ins[0].shape[axis + 1:]
        return [T.broadcast_to(T.reshape(g, kept), ins[0].shape)]
    if op == "reduce_mean":
        axis = node.params["axis"] % len(ins[0].shape)
        kept = ins[0].shape[:axis] + (1,) + ins[0].shape[axis + 1:]
        size = float(ins[0].shape[axis])
        return [T.div(T.broadcast_to(T.reshape(g, kept), ins[0].shape),
                      T.Tensor.scalar(size))]
    if op in ("emit", "cross_sample_sum"):
        return [g]  # identity passthrough (single-sample semantics only)
    raise ValueError(f"no VJP rule for op {op!r}")


def value_and_grad(graph: Graph, wrt, env, loss_output: str | None = None):
    """Forward + reverse sweep.

    Seeds the adjoint of ``loss_output`` with ones, i.e. differentiates the
    sum of the (possibly batched) loss tensor.  Shared inputs therefore
    receive gradients summed across samples; mapped inputs receive
    per-sample gradients.
    """
    loss_output = loss_output or graph.outputs[0]
    values = evaluate(graph, env)
    adj = {loss_output: T.ones_like(values[loss_output])}
    for node in reversed(graph.nodes.values()):
        g = adj.get(node.name)
        if g is None or node.op == "input":
            continue
        for inp, gi in zip(node.inputs, _vjp(node, values, g)):
            if gi is None:
                continue
            adj[inp] = gi if inp not in adj else T.add(adj[inp], gi)
    grads = {w: adj.get(w, T.zeros_like(env[w])) for w in wrt}
    return values[loss_output], grads
