"""Minimal CPU tensor over a flat python list.  No third-party libraries."""

import math
import random
import weakref

from .storage import Storage


def _prod(shape):
    n = 1
    for s in shape:
        n *= s
    return n


def _strides(shape):
    st = [1] * len(shape)
    for i in range(len(shape) - 2, -1, -1):
        st[i] = st[i + 1] * shape[i + 1]
    return tuple(st)


class Tensor:
    """A small dense tensor with a version counter for in-place detection."""

    __slots__ = (
        "shape", "_storage", "_version", "grad", "requires_grad",
        "_node", "name", "_strides", "__weakref__",
    )

    def __init__(self, data, shape=None, requires_grad=False, name=""):
        if isinstance(data, (int, float)):
            data = [float(data)]
        data = [float(x) for x in data]
        if shape is None:
            shape = (len(data),)
        shape = tuple(shape)
        if _prod(shape) != len(data):
            raise ValueError("data length does not match shape")
        self.shape = shape
        self._storage = Storage(data)
        self._version = 0
        self.grad = None
        self.requires_grad = requires_grad
        self._node = None
        self.name = name
        self._strides = _strides(shape)
        weakref.finalize(self, Storage.unpin, self._storage)

    # -- constructors ----------------------------------------------------
    @classmethod
    def scalar(cls, value, **kw):
        return cls([value], shape=(), **kw)

    @classmethod
    def zeros(cls, shape, **kw):
        return cls([0.0] * _prod(tuple(shape)), shape=shape, **kw)

    @classmethod
    def ones(cls, shape, **kw):
        return cls([1.0] * _prod(tuple(shape)), shape=shape, **kw)

    @classmethod
    def rand(cls, shape, seed=0, **kw):
        rng = random.Random(seed)
        return cls([rng.uniform(-1, 1) for _ in range(_prod(tuple(shape)))],
                   shape=shape, **kw)

    # -- basic accessors -------------------------------------------------
    @property
    def data(self):
        return self._storage.data

    @property
    def version(self):
        return self._version

    @property
    def ndim(self):
        return len(self.shape)

    @property
    def size(self):
        return _prod(self.shape)

    def item(self):
        if self.size != 1:
            raise ValueError("only size-1 tensors can be converted to scalars")
        return self.data[0]

    def clone(self):
        return Tensor(list(self.data), shape=self.shape,
                      requires_grad=self.requires_grad, name=self.name)

    # -- in-place mutations (bump the version counter) -------------------
    def _bump(self):
        self._version += 1

    def fill_(self, value):
        for i in range(self.size):
            self.data[i] = float(value)
        self._bump()
        return self

    def zero_(self):
        return self.fill_(0.0)

    def add_(self, other):
        other_data = other.data if isinstance(other, Tensor) else None
        for i in range(self.size):
            self.data[i] += other_data[i] if other_data is not None else float(other)
        self._bump()
        return self

    def mul_(self, other):
        other_data = other.data if isinstance(other, Tensor) else None
        for i in range(self.size):
            self.data[i] *= other_data[i] if other_data is not None else float(other)
        self._bump()
        return self

    # -- autograd-aware operators (defined in ops.py) --------------------
    def __add__(self, other):
        from . import ops
        return ops.add(self, other)

    def __radd__(self, other):
        from . import ops
        return ops.add(self, other)

    def __sub__(self, other):
        from . import ops
        return ops.sub(self, other)

    def __rsub__(self, other):
        from . import ops
        return ops.sub(other, self)

    def __mul__(self, other):
        from . import ops
        return ops.mul(self, other)

    def __rmul__(self, other):
        from . import ops
        return ops.mul(self, other)

    def __truediv__(self, other):
        from . import ops
        return ops.div(self, other)

    def __rtruediv__(self, other):
        from . import ops
        return ops.div(other, self)

    def __neg__(self):
        from . import ops
        return ops.neg(self)

    def __matmul__(self, other):
        from . import ops
        return ops.matmul(self, other)

    def sum(self):
        from . import ops
        return ops.sum_all(self)

    def mean(self):
        from . import ops
        return ops.mean_all(self)

    def exp(self):
        from . import ops
        return ops.exp(self)

    def log(self):
        from . import ops
        return ops.log(self)

    def tanh(self):
        from . import ops
        return ops.tanh(self)

    def relu(self):
        from . import ops
        return ops.relu(self)

    def __repr__(self):
        return f"Tensor(shape={self.shape}, data={self.data}, v{self._version})"
