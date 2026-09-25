"""Minimal reverse-mode autodiff with optional gradient-graph construction.

Pure standard library, CPU only, small dense tensors (flat list + shape).
No autodiff frameworks or tensor libraries are used.

Features
--------
- Reverse-mode automatic differentiation over a dynamically built graph.
- ``create_graph=True``: the backward pass itself is recorded as a graph,
  enabling higher-order derivatives and Hessian-vector products (HVP).
- Shared subgraphs are kept as-is: gradients from all uses of a shared
  node are accumulated into fresh tensors (never mutated in place), so
  previously returned gradient buffers are never polluted.
- Each ``grad`` call recomputes from scratch and *replaces* ``.grad``;
  repeated calls never accumulate residue from earlier calls.
"""

import math

_grad_enabled = True


class no_grad:
    """Context manager disabling graph recording (used for plain backward)."""

    def __enter__(self):
        global _grad_enabled
        self._prev = _grad_enabled
        _grad_enabled = False
        return self

    def __exit__(self, *exc):
        global _grad_enabled
        _grad_enabled = self._prev
        return False


def _prod(shape):
    n = 1
    for s in shape:
        n *= s
    return n


class Tensor:
    """Dense tensor on a flat float list. Shapes: () scalar, (n,), (m, n)."""

    __slots__ = ("data", "shape", "requires_grad", "grad", "_prev", "_backward")

    def __init__(self, data, shape=None, requires_grad=False):
        if isinstance(data, (int, float)):
            data = [float(data)]
            shape = () if shape is None else shape
        self.data = [float(v) for v in data]
        self.shape = tuple(shape) if shape is not None else (len(self.data),)
        if _prod(self.shape) != len(self.data):
            raise ValueError("data length does not match shape")
        self.requires_grad = bool(requires_grad)
        self.grad = None
        self._prev = ()
        self._backward = None

    # -- constructors ----------------------------------------------------
    @staticmethod
    def zeros(shape, requires_grad=False):
        return Tensor([0.0] * _prod(shape), shape, requires_grad)

    # -- convenience -----------------------------------------------------
    def item(self):
        if self.shape != ():
            raise ValueError("item() only works on scalars")
        return self.data[0]

    def clone(self):
        return Tensor(list(self.data), self.shape, self.requires_grad)

    def __repr__(self):
        return f"Tensor(shape={self.shape}, data={self.data})"


def _ensure(value):
    if isinstance(value, Tensor):
        return value
    return Tensor(value)


def _record(out, parents, backward):
    if _grad_enabled and any(p.requires_grad for p in parents):
        out.requires_grad = True
        out._prev = tuple(parents)
        out._backward = backward
    return out


# ----------------------------------------------------------------------
# Primitive ops. Each backward is expressed with the same public ops, so
# it is itself traceable when the grad mode is on (create_graph=True).
# ----------------------------------------------------------------------

def _unbroadcast(g, shape):
    """Reduce a gradient to a broadcast (scalar) source shape."""
    if g.shape == shape:
        return g
    if shape != ():
        raise ValueError("only scalar broadcasting is supported")
    return g.sum()


def add(a, b):
    a, b = _ensure(a), _ensure(b)
    if a.shape == ():
        data, shape = [a.data[0] + v for v in b.data], b.shape
    elif b.shape == ():
        data, shape = [v + b.data[0] for v in a.data], a.shape
    elif a.shape == b.shape:
        data, shape = [x + y for x, y in zip(a.data, b.data)], a.shape
    else:
        raise ValueError(f"shape mismatch: {a.shape} vs {b.shape}")
    out = Tensor(data, shape)

    def _backward(g):
        return _unbroadcast(g, a.shape), _unbroadcast(g, b.shape)

    return _record(out, (a, b), _backward)


def mul(a, b):
    a, b = _ensure(a), _ensure(b)
    if a.shape == ():
        data, shape = [a.data[0] * v for v in b.data], b.shape
    elif b.shape == ():
        data, shape = [v * b.data[0] for v in a.data], a.shape
    elif a.shape == b.shape:
        data, shape = [x * y for x, y in zip(a.data, b.data)], a.shape
    else:
        raise ValueError(f"shape mismatch: {a.shape} vs {b.shape}")
    out = Tensor(data, shape)

    def _backward(g):
        return _unbroadcast(g * b, a.shape), _unbroadcast(g * a, b.shape)

    return _record(out, (a, b), _backward)


def neg(a):
    return mul(a, -1.0)


def sub(a, b):
    return add(a, neg(_ensure(b)))


def sum_all(t):
    out = Tensor(sum(t.data), ())

    def _backward(g):
        return (_expand(g, t.shape),)

    return _record(out, (t,), _backward)


def _expand(g, shape):
    """Broadcast a scalar to ``shape``; its backward sums everything back."""
    out = Tensor([g.data[0]] * _prod(shape), shape)

    def _backward(gg):
        return (gg.sum(),)

    return _record(out, (g,), _backward)


def exp(t):
    out = Tensor([math.exp(v) for v in t.data], t.shape)

    def _backward(g):
        return (g * out,)

    return _record(out, (t,), _backward)


def transpose(t):
    if len(t.shape) != 2:
        raise ValueError("transpose expects a 2-D tensor")
    m, n = t.shape
    out = Tensor([t.data[i * n + j] for j in range(n) for i in range(m)], (n, m))

    def _backward(g):
        return (g.T,)

    return _record(out, (t,), _backward)


def matmul(a, b):
    if len(a.shape) != 2 or len(b.shape) != 2:
        raise ValueError("matmul expects 2-D tensors")
    m, k = a.shape
    k2, n = b.shape
    if k != k2:
        raise ValueError(f"inner dim mismatch: {a.shape} vs {b.shape}")
    data = [
        sum(a.data[i * k + p] * b.data[p * n + j] for p in range(k))
        for i in range(m)
        for j in range(n)
    ]
    out = Tensor(data, (m, n))

    def _backward(g):
        return matmul(g, b.T), matmul(a.T, g)

    return _record(out, (a, b), _backward)


# -- operator sugar ------------------------------------------------------
Tensor.__add__ = add
Tensor.__radd__ = lambda a, b: add(_ensure(b), a)
Tensor.__sub__ = sub
Tensor.__rsub__ = lambda a, b: sub(_ensure(b), a)
Tensor.__mul__ = mul
Tensor.__rmul__ = lambda a, b: mul(_ensure(b), a)
Tensor.__neg__ = neg
Tensor.__matmul__ = matmul
Tensor.sum = sum_all
Tensor.exp = exp
Tensor.T = property(transpose)


# ----------------------------------------------------------------------
# Reverse-mode engine
# ----------------------------------------------------------------------

def _topo(out):
    seen, order = set(), []

    def visit(t):
        if id(t) in seen:
            return
        seen.add(id(t))
        for p in t._prev:
            visit(p)
        order.append(t)

    visit(out)
    return order


def _reverse_sweep(out):
    """Run the reverse sweep; returns {id(tensor): gradient Tensor}.

    Accumulation always builds new tensors (``old + new``); existing
    gradient buffers are never mutated in place.
    """
    grads = {id(out): Tensor(1.0)}
    for t in reversed(_topo(out)):
        g = grads.get(id(t))
        if g is None or t._backward is None:
            continue
        for parent, pg in zip(t._prev, t._backward(g)):
            if not parent.requires_grad:
                continue
            pid = id(parent)
            grads[pid] = grads[pid] + pg if pid in grads else pg
    return grads


def grad(out, inputs, create_graph=False):
    """Gradient of scalar ``out`` w.r.t. ``inputs`` (Tensor or list).

    ``create_graph=True`` records the backward computation itself so the
    returned gradients can be differentiated again (higher order).
    Previous ``.grad`` buffers on the graph are discarded first, so
    repeated calls never accumulate residue.
    """
    single = isinstance(inputs, Tensor)
    inputs = [inputs] if single else list(inputs)
    for t in _topo(out):
        t.grad = None  # drop stale buffers before recomputing
    if create_graph:
        grads = _reverse_sweep(out)
    else:
        with no_grad():
            grads = _reverse_sweep(out)
    results = []
    for t in inputs:
        g = grads.get(id(t))
        if g is None:
            g = Tensor.zeros(t.shape)
        t.grad = g
        results.append(g)
    return results[0] if single else results


def hvp(out, inputs, vectors):
    """Hessian-vector product of scalar ``out`` at ``inputs`` along ``vectors``.

    Builds the gradient graph (create_graph), forms the scalar
    ``sum(grad * v)``, and differentiates it once more.
    """
    single = isinstance(inputs, Tensor)
    inputs = [inputs] if single else list(inputs)
    vectors = [vectors] if single else list(vectors)
    gs = grad(out, inputs, create_graph=True)
    gv = None
    for g, v in zip(gs, vectors):
        term = (g * v).sum()
        gv = term if gv is None else gv + term
    results = grad(gv, inputs)
    return results[0] if single else results
