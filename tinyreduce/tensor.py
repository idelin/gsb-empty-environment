"""Minimal CPU tensor: flat storage + strides, views, tiny reverse-mode engine.

Pure standard library.  No autodiff/tensor dependencies.
"""
from __future__ import annotations

from itertools import product


def _prod(shape):
    result = 1
    for s in shape:
        result *= s
    return result


def c_strides(shape):
    """C-order (row-major) strides, in elements."""
    acc = 1
    out = []
    for s in reversed(shape):
        out.append(acc)
        acc *= s
    return tuple(reversed(out))


class Storage:
    """Shared 1-D buffer backing one or more tensor views.

    `grad` (lazily allocated) and `nondiff` live on the storage so that
    overlapping views accumulate gradients into the same memory and share
    non-differentiable point marks.
    """

    __slots__ = ("data", "grad", "nondiff")

    def __init__(self, data):
        self.data = data
        self.grad = None      # list[float] | None, same length as data
        self.nondiff = set()  # flat positions whose grad is a chosen subgradient


def _flatten(nested):
    """nested lists/tuples of numbers -> (shape, flat float list)."""
    if isinstance(nested, (list, tuple)):
        flat = []
        sub = None
        for item in nested:
            s, f = _flatten(item)
            if sub is None:
                sub = s
            elif s != sub:
                raise ValueError("ragged nested sequences are not a tensor")
            flat.extend(f)
        return (len(nested),) + (sub or ()), flat
    return (), [float(nested)]


def _iadd(a, b):
    """a += b elementwise (same shape)."""
    for idx in a.indices():
        a.set(idx, a.get(idx) + b.get(idx))


class Tensor:
    __slots__ = ("storage", "shape", "strides", "offset",
                 "requires_grad", "_ctx", "_nondiff", "reduction_stats")

    def __init__(self, data=None, shape=None, *, strides=None, offset=0,
                 storage=None, requires_grad=False):
        if storage is not None:
            self.storage = storage
            self.shape = tuple(shape)
            self.strides = tuple(strides)
            self.offset = offset
        else:
            if shape is None:
                shp, flat = _flatten(data)
            else:
                shp = tuple(int(s) for s in shape)
                if isinstance(data, (int, float, bool)):
                    flat = [float(data)] * _prod(shp)
                else:
                    flat = [float(v) for v in data]
                if len(flat) != _prod(shp):
                    raise ValueError(
                        f"data length {len(flat)} does not match shape {shp}")
            self.storage = Storage(flat)
            self.shape = shp
            self.strides = tuple(strides) if strides is not None else c_strides(shp)
            self.offset = offset
        self.requires_grad = requires_grad
        self._ctx = None        # (parents, backward_fn) | None
        self._nondiff = set()   # multi-indices (own index space) marked non-differentiable
        self.reduction_stats = None

    # ------------------------------------------------------------- factories
    @classmethod
    def zeros(cls, shape):
        return cls(0.0, shape)

    @classmethod
    def ones(cls, shape):
        return cls(1.0, shape)

    @classmethod
    def full(cls, shape, value):
        return cls(value, shape)

    # -------------------------------------------------------------- metadata
    @property
    def ndim(self):
        return len(self.shape)

    @property
    def size(self):
        return _prod(self.shape)

    def requires_grad_(self, flag=True):
        self.requires_grad = flag
        return self

    # -------------------------------------------------------------- indexing
    def indices(self):
        """Iterate all multi-indices in C order (empty for zero-size dims)."""
        return product(*(range(s) for s in self.shape))

    def _flat(self, idx):
        return self.offset + sum(i * s for i, s in zip(idx, self.strides))

    def get(self, idx):
        return self.storage.data[self._flat(idx)]

    def set(self, idx, value):
        self.storage.data[self._flat(idx)] = float(value)

    def __getitem__(self, key):
        if not isinstance(key, tuple):
            key = (key,)
        key = list(key)
        if Ellipsis in key:
            i = key.index(Ellipsis)
            n_spec = sum(1 for k in key if k is not Ellipsis)
            key = key[:i] + [slice(None)] * (self.ndim - n_spec) + key[i + 1:]
        if len(key) > self.ndim:
            raise IndexError(
                f"too many indices for tensor of dimension {self.ndim}")
        key = key + [slice(None)] * (self.ndim - len(key))

        new_shape, new_strides = [], []
        off = self.offset
        for dim, k in enumerate(key):
            n = self.shape[dim]
            if isinstance(k, bool):
                raise TypeError("boolean indexing is not supported")
            if isinstance(k, int):
                i = k + n if k < 0 else k
                if not 0 <= i < n:
                    raise IndexError(
                        f"index {k} is out of bounds for axis {dim} with size {n}")
                off += i * self.strides[dim]
            elif isinstance(k, slice):
                start, stop, step = k.indices(n)
                length = len(range(start, stop, step))
                off += start * self.strides[dim]
                new_shape.append(length)
                new_strides.append(self.strides[dim] * step)
            else:
                raise TypeError(f"unsupported index component: {k!r}")
        return self._spawn(tuple(new_shape), tuple(new_strides), off)

    # ----------------------------------------------------------------- views
    def _spawn(self, shape, strides, offset):
        return Tensor(shape=shape, strides=strides, offset=offset,
                      storage=self.storage, requires_grad=self.requires_grad)

    def permute(self, axes):
        axes = tuple(a + self.ndim if a < 0 else a for a in axes)
        if sorted(axes) != list(range(self.ndim)):
            raise ValueError(f"not a permutation of {self.ndim} axes: {axes}")
        return self._spawn(tuple(self.shape[a] for a in axes),
                           tuple(self.strides[a] for a in axes), self.offset)

    def transpose(self, a=0, b=1):
        a %= self.ndim
        b %= self.ndim
        axes = list(range(self.ndim))
        axes[a], axes[b] = axes[b], axes[a]
        return self.permute(axes)

    @property
    def T(self):
        return self.permute(tuple(reversed(range(self.ndim))))

    def is_contiguous(self):
        return self.strides == c_strides(self.shape)

    def reshape(self, shape):
        shape = tuple(shape)
        if _prod(shape) != self.size:
            raise ValueError(f"cannot reshape {self.shape} into {shape}")
        if not self.is_contiguous():
            raise ValueError("reshape requires a contiguous tensor")
        return self._spawn(shape, c_strides(shape), self.offset)

    def contiguous(self):
        if self.is_contiguous() and self.offset == 0 \
                and len(self.storage.data) == self.size:
            return self
        return self.clone()

    def clone(self):
        out = Tensor.zeros(self.shape)
        for idx in self.indices():
            out.set(idx, self.get(idx))
        return out

    # ------------------------------------------------------------- conversion
    def to_flat(self):
        return [self.get(idx) for idx in self.indices()]

    def to_nested(self):
        def build(dim, prefix):
            if dim == self.ndim:
                return self.get(tuple(prefix))
            return [build(dim + 1, prefix + [i]) for i in range(self.shape[dim])]
        return build(0, [])

    def item(self):
        if self.size != 1:
            raise ValueError("only size-1 tensors can be converted to scalars")
        return self.get(next(self.indices()))

    def __repr__(self):
        return f"Tensor({self.to_nested()!r})"

    # ------------------------------------------------------------- reductions
    def sum(self, axis=None, keepdims=False, mask=None, chunk_size=None):
        from .ops import reduce_sum
        return reduce_sum(self, axis=axis, keepdims=keepdims,
                          mask=mask, chunk_size=chunk_size)

    def mean(self, axis=None, keepdims=False, mask=None, chunk_size=None):
        from .ops import reduce_mean
        return reduce_mean(self, axis=axis, keepdims=keepdims,
                           mask=mask, chunk_size=chunk_size)

    def min(self, axis=None, keepdims=False, mask=None, chunk_size=None):
        from .ops import reduce_min
        return reduce_min(self, axis=axis, keepdims=keepdims,
                          mask=mask, chunk_size=chunk_size)

    def max(self, axis=None, keepdims=False, mask=None, chunk_size=None):
        from .ops import reduce_max
        return reduce_max(self, axis=axis, keepdims=keepdims,
                          mask=mask, chunk_size=chunk_size)

    # ----------------------------------------------------------------- grads
    @property
    def grad(self):
        """Gradient view sharing the storage-level gradient buffer.

        Overlapping views therefore accumulate into the same memory.
        """
        st = self.storage
        if st.grad is None:
            st.grad = [0.0] * len(st.data)
        return Tensor(shape=self.shape, strides=self.strides,
                      offset=self.offset, storage=Storage(st.grad))

    def zero_grad(self):
        self.storage.grad = None
        self.storage.nondiff.clear()

    @property
    def nondiff_indices(self):
        """Sorted multi-indices (own index space) marked non-differentiable."""
        own = {idx for idx in self.indices()
               if self._flat(idx) in self.storage.nondiff}
        return tuple(sorted(own | self._nondiff))

    @property
    def has_nondiff(self):
        return bool(self.nondiff_indices)

    def backward(self, upstream=None):
        """Reverse-mode sweep from this tensor, accumulating into leaf grads."""
        if upstream is None:
            if self.size != 1:
                raise ValueError(
                    "upstream gradient required for non-scalar outputs")
            upstream = Tensor(1.0)
        elif not isinstance(upstream, Tensor):
            upstream = Tensor(upstream, self.shape)
        if upstream.shape != self.shape:
            raise ValueError(
                f"upstream shape {upstream.shape} != output shape {self.shape}")

        topo, seen = [], set()

        def build(node):
            if id(node) in seen:
                return
            seen.add(id(node))
            if node._ctx is not None:
                for parent in node._ctx[0]:
                    build(parent)
            topo.append(node)

        build(self)

        accum = {id(self): [self, upstream]}
        for node in reversed(topo):
            entry = accum.get(id(node))
            if entry is None or node._ctx is None:
                continue
            parents, backward_fn = node._ctx
            for parent, pgrad in zip(parents, backward_fn(entry[1])):
                if pgrad is None:
                    continue
                slot = accum.get(id(parent))
                if slot is None:
                    accum[id(parent)] = [parent, pgrad]
                else:
                    _iadd(slot[1], pgrad)
                    slot[1]._nondiff |= pgrad._nondiff

        for node, grad in accum.values():
            if node._ctx is None and node.requires_grad:
                view = node.grad
                for idx in node.indices():
                    view.set(idx, view.get(idx) + grad.get(idx))
                for midx in grad._nondiff:
                    node.storage.nondiff.add(node._flat(midx))
