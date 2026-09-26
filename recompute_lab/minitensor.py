"""Minimal CPU tensor library implemented with the Python standard library only.

No autograd, no third-party tensor libraries.  Tensors are flat python lists of
floats plus shape/stride metadata.  Views share a ``Storage`` object so that
identity (``storage_id``) and mutation (``version``) can be checked when a
computation graph replays or recomputes nodes.
"""
from __future__ import annotations

import math
import random


class Storage:
    """Shared backing buffer.  ``version`` bumps on every in-place mutation."""

    __slots__ = ("data", "version")

    def __init__(self, data):
        self.data = list(data)
        self.version = 0


def _default_strides(shape):
    strides = []
    acc = 1
    for dim in reversed(shape):
        strides.append(acc)
        acc *= dim
    return tuple(reversed(strides))


def _numel(shape):
    n = 1
    for dim in shape:
        n *= dim
    return n


class Tensor:
    """A small dense tensor (1-D or 2-D is all we need here)."""

    __slots__ = ("storage", "shape", "strides", "offset")

    def __init__(self, storage, shape, strides=None, offset=0):
        self.storage = storage
        self.shape = tuple(shape)
        self.strides = tuple(strides) if strides is not None else _default_strides(shape)
        self.offset = offset

    # -- constructors ------------------------------------------------------
    @classmethod
    def zeros(cls, shape):
        return cls(Storage([0.0] * _numel(shape)), shape)

    @classmethod
    def from_list(cls, values, shape=None):
        flat = list(values)
        if shape is None:
            shape = (len(flat),)
        assert _numel(shape) == len(flat), "shape does not match data length"
        return cls(Storage(flat), shape)

    @classmethod
    def rand(cls, shape, rng: random.Random):
        return cls(Storage([rng.random() for _ in range(_numel(shape))]), shape)

    # -- views --------------------------------------------------------------
    def reshape(self, shape):
        """Return a view sharing the same storage (only for contiguous tensors)."""
        shape = tuple(shape)
        assert _numel(shape) == self.numel(), "reshape must preserve element count"
        assert self.is_contiguous(), "only contiguous tensors can be reshaped as a view"
        return Tensor(self.storage, shape, _default_strides(shape), self.offset)

    def is_contiguous(self):
        return self.strides == _default_strides(self.shape)

    @property
    def storage_id(self):
        return id(self.storage)

    @property
    def version(self):
        return self.storage.version

    def numel(self):
        return _numel(self.shape)

    # -- element access ------------------------------------------------------
    def _flat_index(self, idx):
        if not isinstance(idx, tuple):
            idx = (idx,)
        flat = self.offset
        for i, s in zip(idx, self.strides):
            flat += i * s
        return flat

    def __getitem__(self, idx):
        return self.storage.data[self._flat_index(idx)]

    def __setitem__(self, idx, value):
        self.storage.data[self._flat_index(idx)] = float(value)
        self.storage.version += 1

    def fill_(self, value):
        for i in range(self.numel()):
            self.storage.data[self.offset + i] = float(value)
        self.storage.version += 1

    def tolist(self):
        if len(self.shape) == 1:
            return [self[i] for i in range(self.shape[0])]
        if len(self.shape) == 2:
            return [[self[i, j] for j in range(self.shape[1])] for i in range(self.shape[0])]
        raise ValueError("tolist supports 1-D/2-D only")

    def clone(self):
        return Tensor(Storage(self.tolist_flat()), self.shape)

    def tolist_flat(self):
        out = []

        def walk(prefix, dim):
            if dim == len(self.shape):
                out.append(self[tuple(prefix)])
                return
            for i in range(self.shape[dim]):
                walk(prefix + [i], dim + 1)

        walk([], 0)
        return out

    def almost_equal(self, other, tol=1e-9):
        if self.shape != other.shape:
            return False
        return all(
            abs(a - b) <= tol for a, b in zip(self.tolist_flat(), other.tolist_flat())
        )

    def exactly_equal(self, other):
        return self.shape == other.shape and self.tolist_flat() == other.tolist_flat()

    def __repr__(self):
        return f"Tensor(shape={self.shape}, data={self.tolist()})"


# ---------------------------------------------------------------------------
# Elementwise / linear algebra helpers (all allocate fresh storage).
# ---------------------------------------------------------------------------

def _binary(a: Tensor, b: Tensor, fn):
    assert a.shape == b.shape, f"shape mismatch {a.shape} vs {b.shape}"
    out = Tensor.zeros(a.shape)
    for i in range(a.numel()):
        out.storage.data[i] = fn(a.tolist_flat()[i], b.tolist_flat()[i])
    return out


def add(a: Tensor, b: Tensor) -> Tensor:
    return _binary(a, b, lambda x, y: x + y)


def sub(a: Tensor, b: Tensor) -> Tensor:
    return _binary(a, b, lambda x, y: x - y)


def mul(a: Tensor, b: Tensor) -> Tensor:
    return _binary(a, b, lambda x, y: x * y)


def scale(a: Tensor, s: float) -> Tensor:
    out = Tensor.zeros(a.shape)
    flat = a.tolist_flat()
    for i in range(a.numel()):
        out.storage.data[i] = flat[i] * s
    return out


def add_inplace(a: Tensor, b: Tensor) -> None:
    assert a.shape == b.shape
    bf = b.tolist_flat()
    for i in range(a.numel()):
        a.storage.data[a.offset + i] += bf[i]
    a.storage.version += 1


def relu(a: Tensor) -> Tensor:
    out = Tensor.zeros(a.shape)
    flat = a.tolist_flat()
    for i in range(a.numel()):
        out.storage.data[i] = flat[i] if flat[i] > 0.0 else 0.0
    return out


def tanh(a: Tensor) -> Tensor:
    out = Tensor.zeros(a.shape)
    flat = a.tolist_flat()
    for i in range(a.numel()):
        out.storage.data[i] = math.tanh(flat[i])
    return out


def matmul(a: Tensor, b: Tensor) -> Tensor:
    assert len(a.shape) == 2 and len(b.shape) == 2
    m, k = a.shape
    k2, n = b.shape
    assert k == k2, f"matmul inner dim mismatch {k} vs {k2}"
    out = Tensor.zeros((m, n))
    for i in range(m):
        for j in range(n):
            acc = 0.0
            for p in range(k):
                acc += a[i, p] * b[p, j]
            out[i, j] = acc
    return out


def sum_all(a: Tensor) -> Tensor:
    return Tensor.from_list([sum(a.tolist_flat())])


def dropout(a: Tensor, p: float, rng: random.Random):
    """Random op: zero each element with probability ``p`` (inverted dropout).

    Returns (output, mask).  The mask is needed for the backward pass, and the
    output depends on ``rng`` state, so deterministic recompute must restore an
    identical rng state.
    """
    mask = Tensor.zeros(a.shape)
    out = Tensor.zeros(a.shape)
    flat = a.tolist_flat()
    keep = 1.0 / (1.0 - p)
    for i in range(a.numel()):
        m = 0.0 if rng.random() < p else keep
        mask.storage.data[i] = m
        out.storage.data[i] = flat[i] * m
    return out, mask


def transpose(a: Tensor) -> Tensor:
    assert len(a.shape) == 2
    out = Tensor.zeros((a.shape[1], a.shape[0]))
    for i in range(a.shape[0]):
        for j in range(a.shape[1]):
            out[j, i] = a[i, j]
    return out


def ones(shape) -> Tensor:
    t = Tensor.zeros(shape)
    t.fill_(1.0)
    return t


def full(shape, value) -> Tensor:
    t = Tensor.zeros(shape)
    t.fill_(value)
    return t
