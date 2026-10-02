"""Shape-polymorphic compilation cache.

``compile(fn)`` traces ``fn`` once per *shape-constraint structure* instead
of once per concrete shape.  During tracing every argument dimension is a
symbolic variable; broadcasting unifies variables and records guards such as
``arg0.dim1 == arg1.dim0`` or ``arg0.dim1 == 3``.  A dimension that never
participates in a constraint (e.g. a batch dimension) stays free, so the
same plan is reused for batch size 2, 5 or 0.  Plans are replayed on
concrete tensors, which builds a fresh autograd graph on every call.
"""

from . import autograd as ag
from .autograd import Tensor
from .ndarray import NDArray, ShapeError


# ------------------------------------------------------------ symbolic dims
class _Dim:
    __slots__ = ("parent", "const", "token")

    def __init__(self, token=None, const=None):
        self.parent = self
        self.const = const
        self.token = token  # ('arg', arg_index, axis) for argument dims


def _find(d):
    while d.parent is not d:
        d.parent = d.parent.parent
        d = d.parent
    return d


# --------------------------------------------------------------------- plan
class _Plan:
    def __init__(self, ranks, guards, instrs, consts, out_refs, out_is_seq):
        self.ranks = tuple(ranks)
        self.guards = frozenset(guards)
        self.instrs = instrs
        self.consts = consts
        self.out_refs = out_refs
        self.out_is_seq = out_is_seq

    def signature(self):
        return (self.ranks, self.guards)

    def matches(self, args):
        if len(args) != len(self.ranks):
            return False
        for a, r in zip(args, self.ranks):
            if a.ndim != r:
                return False
        for g in self.guards:
            if g[0] == "const":
                _, (i, j), k = g
                if args[i].shape[j] != k:
                    return False
            else:  # 'eq'
                _, (i, j), (k, l) = g
                if args[i].shape[j] != args[k].shape[l]:
                    return False
        return True

    def execute(self, args):
        tmps = []

        def resolve(ref):
            kind, i = ref
            if kind == "arg":
                return args[i]
            if kind == "tmp":
                return tmps[i]
            return self.consts[i]  # float or NDArray; ops coerce via _ensure

        for name, refs, params in self.instrs:
            tmps.append(_OPS[name](*[resolve(r) for r in refs], **params))
        outs = [resolve(r) for r in self.out_refs]
        return tuple(outs) if self.out_is_seq else outs[0]


_OPS = {
    "add": lambda a, b: ag.add(a, b),
    "sub": lambda a, b: ag.sub(a, b),
    "mul": lambda a, b: ag.mul(a, b),
    "div": lambda a, b: ag.div(a, b),
    "neg": lambda a: ag.neg(a),
    "exp": lambda a: ag.exp(a),
    "log": lambda a: ag.log(a),
    "tanh": lambda a: ag.tanh(a),
    "sum": lambda a, axis, keepdims: ag.sum(a, axis=axis, keepdims=keepdims),
    "permute": lambda a, perm: ag.permute(a, perm),
    "broadcast_to": lambda a, shape: ag.broadcast_to(a, shape),
    "unsqueeze": lambda a, axes: ag.unsqueeze(a, axes),
    "squeeze": lambda a, axes: ag.squeeze(a, axes),
}


# ------------------------------------------------------------------- tracer
class _Tracer:
    def __init__(self):
        self.guards = []
        self.instrs = []
        self.consts = []

    def record(self, name, refs, params):
        self.instrs.append((name, refs, params))
        return ("tmp", len(self.instrs) - 1)

    def guard(self, g):
        if g not in self.guards:
            self.guards.append(g)

    def union(self, a, b):
        ra, rb = _find(a), _find(b)
        if ra is rb:
            return ra
        ca, cb = ra.const, rb.const
        if ca is not None and cb is not None and ca != cb:
            raise ShapeError("dimension mismatch: %d vs %d" % (ca, cb))
        if ca is not None and rb.token is not None:
            self.guard(("const", rb.token, ca))
        if cb is not None and ra.token is not None:
            self.guard(("const", ra.token, cb))
        if ca is None and cb is None and ra.token and rb.token:
            self.guard(("eq", ra.token, rb.token))
        rb.parent = ra
        if ca is None:
            ra.const = cb
        if ra.token is None:
            ra.token = rb.token
        return ra

    def merge_dim(self, a, b):
        """Broadcast-merge two symbolic dimensions."""
        ra, rb = _find(a), _find(b)
        if ra is rb:
            return ra
        if ra.const == 1:
            return rb
        if rb.const == 1:
            return ra
        return self.union(a, b)

    def broadcast_dims(self, da, db):
        nd = max(len(da), len(db))
        pa = tuple(_Dim(const=1) for _ in range(nd - len(da))) + da
        pb = tuple(_Dim(const=1) for _ in range(nd - len(db))) + db
        return tuple(self.merge_dim(x, y) for x, y in zip(pa, pb))

    def coerce(self, x):
        """Return (ref, dims) for a tracer tensor, Tensor, NDArray or number."""
        if isinstance(x, _TracerTensor):
            return x.ref, x.dims
        if isinstance(x, Tensor):
            x = x.data
        if isinstance(x, NDArray):
            self.consts.append(x)
            return ("const", len(self.consts) - 1), tuple(
                _Dim(const=s) for s in x.shape)
        if isinstance(x, (int, float)):
            self.consts.append(float(x))
            return ("const", len(self.consts) - 1), ()
        raise TypeError("unsupported operand type: %r" % type(x))


class _TracerTensor:
    __slots__ = ("tr", "dims", "ref")

    def __init__(self, tr, dims, ref):
        self.tr = tr
        self.dims = dims
        self.ref = ref

    # ---------------------------------------------------------------- binary
    def _binary(self, other, name, swap=False):
        oref, odims = self.tr.coerce(other)
        dims = self.tr.broadcast_dims(self.dims, odims)
        refs = (oref, self.ref) if swap else (self.ref, oref)
        return _TracerTensor(self.tr, dims, self.tr.record(name, refs, {}))

    def __add__(self, o):
        return self._binary(o, "add")

    def __radd__(self, o):
        return self._binary(o, "add", swap=True)

    def __sub__(self, o):
        return self._binary(o, "sub")

    def __rsub__(self, o):
        return self._binary(o, "sub", swap=True)

    def __mul__(self, o):
        return self._binary(o, "mul")

    def __rmul__(self, o):
        return self._binary(o, "mul", swap=True)

    def __truediv__(self, o):
        return self._binary(o, "div")

    def __rtruediv__(self, o):
        return self._binary(o, "div", swap=True)

    # ----------------------------------------------------------------- unary
    def _unary(self, name):
        return _TracerTensor(self.tr, self.dims,
                             self.tr.record(name, (self.ref,), {}))

    def __neg__(self):
        return self._unary("neg")

    def exp(self):
        return self._unary("exp")

    def log(self):
        return self._unary("log")

    def tanh(self):
        return self._unary("tanh")

    # ------------------------------------------------------------- reshaping
    def sum(self, axis=None, keepdims=False):
        nd = len(self.dims)
        if axis is None:
            axes = None
            dims = tuple(_Dim(const=1) for _ in range(nd)) if keepdims else ()
        else:
            axes = (axis,) if isinstance(axis, int) else tuple(axis)
            axes = tuple(ax % nd for ax in axes)
            drop = set(axes)
            if keepdims:
                dims = tuple(_Dim(const=1) if i in drop else d
                             for i, d in enumerate(self.dims))
            else:
                dims = tuple(d for i, d in enumerate(self.dims)
                             if i not in drop)
        ref = self.tr.record("sum", (self.ref,),
                             {"axis": axes, "keepdims": keepdims})
        return _TracerTensor(self.tr, dims, ref)

    def permute(self, perm):
        nd = len(self.dims)
        perm = tuple(p % nd for p in perm)
        ref = self.tr.record("permute", (self.ref,), {"perm": perm})
        return _TracerTensor(self.tr, tuple(self.dims[i] for i in perm), ref)

    def transpose(self):
        return self.permute(tuple(reversed(range(len(self.dims)))))

    @property
    def T(self):
        return self.transpose()

    def broadcast_to(self, shape):
        shape = tuple(int(s) for s in shape)
        const_dims = tuple(_Dim(const=s) for s in shape)
        dims = self.tr.broadcast_dims(self.dims, const_dims)
        ref = self.tr.record("broadcast_to", (self.ref,), {"shape": shape})
        return _TracerTensor(self.tr, dims, ref)

    def unsqueeze(self, axes):
        axes = tuple(sorted(axes))
        dims = list(self.dims)
        for ax in axes:
            dims.insert(ax, _Dim(const=1))
        ref = self.tr.record("unsqueeze", (self.ref,), {"axes": axes})
        return _TracerTensor(self.tr, tuple(dims), ref)


def _trace(fn, ranks):
    tr = _Tracer()
    args = [_TracerTensor(tr,
                          tuple(_Dim(token=("arg", i, j)) for j in range(r)),
                          ("arg", i))
            for i, r in enumerate(ranks)]
    out = fn(*args)
    if isinstance(out, (list, tuple)):
        out_refs = [o.ref for o in out]
        is_seq = True
    else:
        out_refs = [out.ref]
        is_seq = False
    return _Plan(ranks, tr.guards, tr.instrs, tr.consts, out_refs, is_seq)


# ------------------------------------------------------------------ compiler
class CompiledFunction:
    def __init__(self, fn):
        self.fn = fn
        self.plans = []

    def __call__(self, *args):
        args = tuple(a if isinstance(a, Tensor) else Tensor(a) for a in args)
        for p in self.plans:
            if p.matches(args):
                return p.execute(args)
        plan = _trace(self.fn, [a.ndim for a in args])
        for p in self.plans:
            if p.signature() == plan.signature():
                # Same constraint structure: reuse the cached plan.  If the
                # concrete shapes violate its guards, execution raises the
                # shape error with the original plan.
                return p.execute(args)
        self.plans.append(plan)
        return plan.execute(args)

    @property
    def cache_size(self):
        return len(self.plans)


def compile(fn):
    """Trace ``fn`` per shape-constraint structure and cache the plans."""
    return CompiledFunction(fn)
