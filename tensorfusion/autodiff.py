"""Hand-rolled reverse-mode automatic differentiation over the Program tape.

No external autograd or tensor library is used; every local gradient is
derived and implemented manually.  Backward is supported for elementwise
ops and reduce_sum (store/mutate are rejected: they are runtime effects,
not differentiable graph nodes in this model).
"""

from __future__ import annotations

from .tensor import Tensor, broadcast_shape, iter_indices, project_index


def _binop(x, y, fn):
    shape = broadcast_shape(x.shape, y.shape)
    xs = x.aligned_strides(shape)
    ys = y.aligned_strides(shape)
    out = Tensor.full(shape, 0.0)
    for idx in iter_indices(shape):
        out._write(idx, fn(x.get(project_index(idx, x.shape, xs)),
                           y.get(project_index(idx, y.shape, ys))))
    return out


def unbroadcast(grad, in_shape):
    """Sum a broadcast gradient back down to the operand's shape."""
    if grad.shape == in_shape:
        return grad
    pad = len(grad.shape) - len(in_shape)
    out = Tensor.full(in_shape, 0.0)
    for idx in grad.indices():
        sub = idx[pad:]
        proj = tuple(0 if s == 1 else i for i, s in zip(sub, in_shape))
        out._write(proj, out.get(proj) + grad.get(idx))
    return out


def _add_inplace(acc, delta):
    for idx in acc.indices():
        acc._write(idx, acc.get(idx) + delta.get(idx))


def backward(program, values, seed_var):
    """Return {var: Tensor} gradients of sum(seed_var) w.r.t. inputs."""
    grads = {}

    def accumulate(var, delta):
        delta = unbroadcast(delta, values[var].shape)
        if var in grads:
            _add_inplace(grads[var], delta)
        else:
            grads[var] = delta

    accumulate(seed_var, Tensor.full(values[seed_var].shape, 1.0))
    for op in reversed(program.ops):
        grad = grads.get(op.result)
        if grad is None:
            continue
        if op.kind == "add":
            accumulate(op.args[0], grad)
            accumulate(op.args[1], grad)
        elif op.kind == "sub":
            accumulate(op.args[0], grad)
            accumulate(op.args[1], _binop(grad, grad, lambda g, _: -g))
        elif op.kind == "mul":
            a, b = (values[x] for x in op.args)
            accumulate(op.args[0], _binop(grad, b, lambda g, v: g * v))
            accumulate(op.args[1], _binop(grad, a, lambda g, v: g * v))
        elif op.kind == "div":
            a, b = (values[x] for x in op.args)
            accumulate(op.args[0], _binop(grad, b, lambda g, v: g / v))
            accumulate(op.args[1], _binop(_binop(grad, a, lambda g, v: g * v),
                                          _binop(b, b, lambda u, w: u * w),
                                          lambda ga, b2: -ga / b2))
        elif op.kind == "neg":
            accumulate(op.args[0], _binop(grad, grad, lambda g, _: -g))
        elif op.kind == "reduce_sum":
            in_shape = values[op.args[0]].shape
            expanded = Tensor.full(in_shape, 0.0)
            for idx in expanded.indices():
                expanded._write(idx, grad.get(()))
            accumulate(op.args[0], expanded)
        else:
            raise NotImplementedError(
                f"backward not defined for op kind {op.kind!r}")
    return grads
