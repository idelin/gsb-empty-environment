"""Minimal dense tensor library on pure-Python lists (no third-party deps).

Only what the checkpointing engine needs: elementwise ops, matmul, tanh/relu,
reductions, plus views with identity/version checking for shared storage.
"""
from __future__ import annotations

import math


class VersionError(RuntimeError):
    """Raised when a tensor view is read after its storage was mutated."""


class Storage:
    """Shared backing buffer for tensors; carries a mutation version."""

    _next_id = 0

    def __init__(self, data):
        self.data = list(data)
        self.version = 0
        self.id = Storage._next_id
        Storage._next_id += 1


def numel(shape):
    n = 1
    for s in shape:
        n *= s
    return n


def nbytes_of(shape):
    return 8 * numel(shape)


class Tensor:
    __slots__ = ("storage", "shape", "offset", "_version")

    def __init__(self, data, shape, storage=None, offset=0):
        shape = tuple(shape)
        if storage is None:
            storage = Storage(data)
        if len(storage.data) - offset < numel(shape):
            raise ValueError("storage too small for shape")
        self.storage = storage
        self.shape = shape
        self.offset = offset
        self._version = storage.version

    # -- construction -------------------------------------------------
    @staticmethod
    def zeros(shape):
        return Tensor([0.0] * numel(shape), shape)

    @staticmethod
    def full(shape, value):
        return Tensor([float(value)] * numel(shape), shape)

    @staticmethod
    def randn(shape, rng, scale=1.0):
        return Tensor([rng.gauss(0.0, scale) for _ in range(numel(shape))], shape)

    # -- views / versioning -------------------------------------------
    def view(self, shape=None):
        """Return a tensor sharing this tensor's storage (no copy)."""
        shape = self.shape if shape is None else tuple(shape)
        if numel(shape) != numel(self.shape):
            raise ValueError("view shape must have same numel")
        t = Tensor.__new__(Tensor)
        t.storage = self.storage
        t.shape = shape
        t.offset = self.offset
        t._version = self.storage.version
        return t

    def is_view_of(self, other):
        """Identity check: do the two tensors share the same storage?"""
        return self.storage is other.storage

    def check_version(self):
        if self._version != self.storage.version:
            raise VersionError(
                "stale tensor view: shared storage was mutated in place "
                f"(expected version {self._version}, found {self.storage.version})")

    def add_(self, other):
        """In-place add; bumps the storage version (invalidates views)."""
        self.check_version()
        od = other.data()
        for k in range(numel(self.shape)):
            self.storage.data[self.offset + k] += od[k]
        self.storage.version += 1
        self._version = self.storage.version
        return self

    # -- access ---------------------------------------------------------
    @property
    def nbytes(self):
        return 8 * numel(self.shape)

    def data(self):
        self.check_version()
        n = numel(self.shape)
        return self.storage.data[self.offset:self.offset + n]

    def clone(self):
        return Tensor(self.data(), self.shape)

    def __repr__(self):
        return f"Tensor(shape={self.shape}, data={self.data()!r})"


# ---------------------------------------------------------------------------
# functional ops (allocate new tensors)
# ---------------------------------------------------------------------------

def _elemwise(a, b, fn):
    if a.shape != b.shape:
        raise ValueError(f"shape mismatch {a.shape} vs {b.shape}")
    da, db = a.data(), b.data()
    return Tensor([fn(x, y) for x, y in zip(da, db)], a.shape)


def add(a, b):
    return _elemwise(a, b, lambda x, y: x + y)


def sub(a, b):
    return _elemwise(a, b, lambda x, y: x - y)


def mul(a, b):
    return _elemwise(a, b, lambda x, y: x * y)


def transpose(a):
    if len(a.shape) != 2:
        raise ValueError("transpose expects 2-D")
    m, n = a.shape
    da = a.data()
    return Tensor([da[i * n + j] for j in range(n) for i in range(m)], (n, m))


def matmul(a, b):
    if len(a.shape) != 2 or len(b.shape) != 2 or a.shape[1] != b.shape[0]:
        raise ValueError(f"matmul shape mismatch {a.shape} @ {b.shape}")
    m, k = a.shape
    _, n = b.shape
    da, db = a.data(), b.data()
    out = [0.0] * (m * n)
    for i in range(m):
        for j in range(n):
            acc = 0.0
            for t in range(k):
                acc += da[i * k + t] * db[t * n + j]
            out[i * n + j] = acc
    return Tensor(out, (m, n))


def relu(a):
    return Tensor([x if x > 0.0 else 0.0 for x in a.data()], a.shape)


def tanh(a):
    return Tensor([math.tanh(x) for x in a.data()], a.shape)


def sum_all(a):
    return Tensor([sum(a.data())], ())


def add_noise(rng, a, scale):
    """Stochastic op: adds fresh Gaussian noise drawn from `rng`."""
    return Tensor([x + rng.gauss(0.0, scale) for x in a.data()], a.shape)


# ---------------------------------------------------------------------------
# backward functions (each returns the list of input gradients)
# ---------------------------------------------------------------------------

def add_backward(g, a, b):
    return [g.clone(), g.clone()]


def mul_backward(g, a, b):
    return [mul(g, b), mul(g, a)]


def matmul_backward(g, a, b):
    return [matmul(g, transpose(b)), matmul(transpose(a), g)]


def relu_backward(g, inp):
    di = inp.data()
    dg = g.data()
    return [Tensor([dg[k] if di[k] > 0.0 else 0.0 for k in range(len(dg))], g.shape)]


def tanh_backward(g, out):
    do = out.data()
    dg = g.data()
    return [Tensor([dg[k] * (1.0 - do[k] * do[k]) for k in range(len(dg))], g.shape)]


def sum_all_backward(g, like):
    return [Tensor.full(like.shape, g.data()[0])]


def noise_backward(g, a):
    return [g.clone()]
