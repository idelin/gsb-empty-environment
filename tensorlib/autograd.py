"""Reverse-mode automatic differentiation over :class:`NDArray`.

Features
--------
* broadcasting gradient reduction (``sum_to_shape`` is the adjoint of
  ``broadcast_to``);
* gradient accumulation for shared subgraphs (a variable used twice gets
  both contributions, never an overwrite);
* version counters on shared storage: in-place modification of any tensor
  saved for backward (directly or through a strided view) is rejected;
* ``create_graph=True`` builds a differentiable gradient graph, enabling
  Hessian-vector products;
* explicit graph release: a freed graph refuses to backpropagate again.
"""

import math

from .ndarray import NDArray, ShapeError, _iter_indices


class InplaceModificationError(RuntimeError):
    """A tensor saved for backward was modified in-place."""


class GraphFreedError(RuntimeError):
    """backward()/grad() called on a graph that was already released."""


# ------------------------------------------------------------------ grad mode
_grad_stack = [True]


def is_grad_enabled():
    return _grad_stack[-1]


class _GradMode:
    def __init__(self, enabled):
        self.enabled = enabled

    def __enter__(self):
        _grad_stack.append(self.enabled)
        return self

    def __exit__(self, *exc):
        _grad_stack.pop()
        return False


def no_grad():
    return _GradMode(False)


def enable_grad():
    return _GradMode(True)


# --------------------------------------------------------------------- context
class _Context:
    __slots__ = ("parents", "backward_fn", "saved")

    def __init__(self, parents, backward_fn, saved):
        self.parents = parents
        self.backward_fn = backward_fn
        self.saved = saved  # list of (tensor, version_at_save)

    def check_versions(self):
        for t, v in self.saved:
            if t.data.version != v:
                raise InplaceModificationError(
                    "a tensor saved for backward was modified in-place "
                    "(expected version %d, found version %d)"
                    % (v, t.data.version))


# ---------------------------------------------------------------------- tensor
class Tensor:
    __slots__ = ("data", "requires_grad", "grad",
                 "_ctx", "_released", "_had_ctx")

    def __init__(self, data, requires_grad=False):
        if isinstance(data, Tensor):
            data = data.data
        if not isinstance(data, NDArray):
            data = NDArray.from_nested(data)
        self.data = data
        self.requires_grad = bool(requires_grad)
        self.grad = None
        self._ctx = None
        self._released = False
        self._had_ctx = False

    # ------------------------------------------------------------ properties
    @property
    def shape(self):
        return self.data.shape

    @property
    def ndim(self):
        return self.data.ndim

    @property
    def is_leaf(self):
        return self._ctx is None

    # ---------------------------------------------------------------- helpers
    def to_nested(self):
        return self.data.to_nested()

    def item(self):
        if self.shape != ():
            raise ShapeError("item() requires a scalar tensor")
        return self.data.get(())

    def fill_(self, value):
        """In-place write; bumps the shared storage version counter."""
        for idx in _iter_indices(self.data.shape):
            self.data.set(idx, float(value))
        self.data.storage.version += 1
        return self

    def zero_(self):
        return self.fill_(0.0)

    # ------------------------------------------------------------------- ops
    def __add__(self, other):
        return add(self, other)

    def __radd__(self, other):
        return add(other, self)

    def __sub__(self, other):
        return sub(self, other)

    def __rsub__(self, other):
        return sub(other, self)

    def __mul__(self, other):
        return mul(self, other)

    def __rmul__(self, other):
        return mul(other, self)

    def __truediv__(self, other):
        return div(self, other)

    def __rtruediv__(self, other):
        return div(other, self)

    def __neg__(self):
        return neg(self)

    def exp(self):
        return exp(self)

    def log(self):
        return log(self)

    def tanh(self):
        return tanh(self)

    def sum(self, axis=None, keepdims=False):
        return sum(self, axis=axis, keepdims=keepdims)

    def permute(self, dims):
        return permute(self, dims)

    def transpose(self):
        return transpose(self)

    @property
    def T(self):
        return transpose(self)

    def broadcast_to(self, shape):
        return broadcast_to(self, shape)

    def unsqueeze(self, axes):
        return unsqueeze(self, axes)

    # -------------------------------------------------------------- backward
    def backward(self, grad=None, retain_graph=False, create_graph=False):
        _check_live(self)
        g = _normalize_grad(self, grad)
        topo, grads = _run_backward([self], [g], create_graph)
        for t in topo:
            if t._ctx is None and t.requires_grad:
                lg = grads.get(id(t))
                if lg is not None:
                    if t.grad is None:
                        t.grad = lg
                    else:
                        t.grad = Tensor(t.grad.data.zip(
                            lg.data, lambda x, y: x + y))
        if not (retain_graph or create_graph):
            _release(topo, [self])

    def __repr__(self):
        return "Tensor(shape=%s, requires_grad=%s, data=%s)" % (
            self.shape, self.requires_grad, self.to_nested())


def _ensure(x):
    if isinstance(x, Tensor):
        return x
    return Tensor(x)


def _attach(out, parents, backward_fn, saved=()):
    if is_grad_enabled() and any(p.requires_grad for p in parents):
        out.requires_grad = True
        out._ctx = _Context(parents, backward_fn,
                            [(t, t.data.version) for t in saved])
        out._had_ctx = True
    return out


# ------------------------------------------------------------------------ ops
def add(a, b):
    a = _ensure(a)
    b = _ensure(b)
    out = Tensor(a.data.zip(b.data, lambda x, y: x + y))

    def bwd(g):
        return sum_to_shape(g, a.shape), sum_to_shape(g, b.shape)

    return _attach(out, (a, b), bwd)


def neg(a):
    a = _ensure(a)
    out = Tensor(a.data.map(lambda x: -x))
    return _attach(out, (a,), lambda g: (neg(g),))


def sub(a, b):
    return add(a, neg(b))


def mul(a, b):
    a = _ensure(a)
    b = _ensure(b)
    out = Tensor(a.data.zip(b.data, lambda x, y: x * y))

    def bwd(g):
        return (sum_to_shape(g * b, a.shape),
                sum_to_shape(g * a, b.shape))

    return _attach(out, (a, b), bwd, saved=(a, b))


def div(a, b):
    a = _ensure(a)
    b = _ensure(b)
    out = Tensor(a.data.zip(b.data, lambda x, y: x / y))

    def bwd(g):
        return (sum_to_shape(g / b, a.shape),
                sum_to_shape(neg(g * a / (b * b)), b.shape))

    return _attach(out, (a, b), bwd, saved=(a, b))


def exp(a):
    a = _ensure(a)
    out = Tensor(a.data.map(math.exp))
    return _attach(out, (a,), lambda g: (g * out,), saved=(out,))


def log(a):
    a = _ensure(a)
    out = Tensor(a.data.map(math.log))
    return _attach(out, (a,), lambda g: (g / a,), saved=(a,))


def tanh(a):
    a = _ensure(a)
    out = Tensor(a.data.map(math.tanh))

    def bwd(g):
        return (g * (1.0 - out * out),)

    return _attach(out, (a,), bwd, saved=(out,))


def sum(a, axis=None, keepdims=False):
    a = _ensure(a)
    if axis is None:
        axes = None
    else:
        axes = (axis,) if isinstance(axis, int) else tuple(axis)
        axes = tuple(ax % a.ndim for ax in axes)
    out = Tensor(a.data.sum(axis=axes, keepdims=keepdims))

    def bwd(g):
        gg = g
        if axes is not None and not keepdims:
            gg = unsqueeze(gg, axes)
        return (broadcast_to(gg, a.shape),)

    return _attach(out, (a,), bwd)


def permute(a, dims):
    a = _ensure(a)
    dims = tuple(d % a.ndim for d in dims)
    inv = tuple(sorted(range(a.ndim), key=lambda i: dims[i]))
    out = Tensor(a.data.permute(dims))
    return _attach(out, (a,), lambda g: (permute(g, inv),))


def transpose(a):
    a = _ensure(a)
    return permute(a, tuple(reversed(range(a.ndim))))


def unsqueeze(a, axes):
    a = _ensure(a)
    axes = tuple(sorted(axes))
    out = Tensor(a.data.unsqueeze(axes))
    return _attach(out, (a,), lambda g: (squeeze(g, axes),))


def squeeze(a, axes):
    a = _ensure(a)
    axes = tuple(sorted(axes))
    out = Tensor(a.data.squeeze(axes))
    return _attach(out, (a,), lambda g: (unsqueeze(g, axes),))


def broadcast_to(a, shape):
    a = _ensure(a)
    shape = tuple(int(s) for s in shape)
    out = Tensor(a.data.broadcast_to(shape))
    return _attach(out, (a,), lambda g: (sum_to_shape(g, a.shape),))


def sum_to_shape(a, shape):
    a = _ensure(a)
    shape = tuple(int(s) for s in shape)
    out = Tensor(a.data.sum_to_shape(shape))
    return _attach(out, (a,), lambda g: (broadcast_to(g, a.shape),))


# ------------------------------------------------------------------ backward
def _topo(roots):
    order = []
    seen = set()

    def visit(t):
        if id(t) in seen:
            return
        seen.add(id(t))
        if t._ctx is not None:
            for p in t._ctx.parents:
                visit(p)
        order.append(t)

    for r in roots:
        visit(r)
    return order


def _run_backward(roots, root_grads, create_graph):
    topo = _topo(roots)
    grads = {}
    with _GradMode(create_graph):
        for r, g in zip(roots, root_grads):
            if id(r) in grads:
                grads[id(r)] = grads[id(r)] + g
            else:
                grads[id(r)] = g
        for t in reversed(topo):
            g = grads.get(id(t))
            if g is None or t._ctx is None:
                continue
            t._ctx.check_versions()
            for p, pg in zip(t._ctx.parents, t._ctx.backward_fn(g)):
                if pg is None or not p.requires_grad:
                    continue
                if id(p) in grads:
                    grads[id(p)] = grads[id(p)] + pg
                else:
                    grads[id(p)] = pg
    return topo, grads


def _release(topo, roots):
    for t in topo:
        t._ctx = None
    for r in roots:
        r._released = True


def _check_live(t):
    if t._released or (t._had_ctx and t._ctx is None):
        raise GraphFreedError(
            "trying to backward through a graph that was already freed; "
            "pass retain_graph=True to keep it alive")


def _normalize_grad(out, g):
    if g is None:
        if out.shape != ():
            raise ShapeError(
                "grad can be implicitly created only for scalar outputs")
        return Tensor(NDArray.full((), 1.0))
    g = _ensure(g)
    if g.shape != out.shape:
        raise ShapeError("grad shape %s does not match output shape %s"
                         % (g.shape, out.shape))
    return g


def grad(outputs, inputs, grad_outputs=None, create_graph=False,
         retain_graph=None):
    """Compute gradients of ``outputs`` w.r.t. ``inputs`` and return them as
    a tuple (``None`` for inputs that do not participate in the graph).
    With ``create_graph=True`` the returned gradients are themselves
    differentiable, which enables Hessian-vector products."""
    if isinstance(outputs, Tensor):
        outputs = [outputs]
    else:
        outputs = list(outputs)
    inputs = [inputs] if isinstance(inputs, Tensor) else list(inputs)
    for o in outputs:
        _check_live(o)
    if grad_outputs is None:
        gos = [_normalize_grad(o, None) for o in outputs]
    else:
        if len(outputs) == 1:
            grad_outputs = [grad_outputs]
        if len(grad_outputs) != len(outputs):
            raise ShapeError("grad_outputs must match outputs")
        gos = [_normalize_grad(o, g) for o, g in zip(outputs, grad_outputs)]
    if retain_graph is None:
        retain_graph = create_graph
    topo, grads = _run_backward(outputs, gos, create_graph)
    result = tuple(grads.get(id(t)) for t in inputs)
    if not (retain_graph or create_graph):
        _release(topo, outputs)
    return result
