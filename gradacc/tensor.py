"""Minimal CPU tensor over a flat float list. Standard library only."""

from __future__ import annotations

import math


def _prod(shape):
    n = 1
    for d in shape:
        n *= d
    return n


class Tensor:
    """Tiny dense tensor: flat list of floats plus a shape tuple."""

    __slots__ = ("data", "shape")

    def __init__(self, data, shape):
        self.shape = tuple(int(d) for d in shape)
        flat = [float(v) for v in data]
        if len(flat) != _prod(self.shape):
            raise ValueError(
                f"data length {len(flat)} does not match shape {self.shape}")
        self.data = flat

    @classmethod
    def zeros(cls, shape):
        shape = tuple(shape)
        return cls([0.0] * _prod(shape), shape)

    @classmethod
    def from_nested(cls, nested):
        if not isinstance(nested, (list, tuple)):
            return cls([nested], ())
        if nested and isinstance(nested[0], (list, tuple)):
            rows = len(nested)
            cols = len(nested[0])
            flat = []
            for row in nested:
                if len(row) != cols:
                    raise ValueError("ragged nested data")
                flat.extend(row)
            return cls(flat, (rows, cols))
        return cls(list(nested), (len(nested),))

    @property
    def size(self):
        return len(self.data)

    def copy(self):
        return Tensor(list(self.data), self.shape)

    def isfinite(self):
        return all(math.isfinite(v) for v in self.data)

    def _check(self, other):
        if self.shape != other.shape:
            raise ValueError(f"shape mismatch {self.shape} vs {other.shape}")

    def __add__(self, other):
        self._check(other)
        return Tensor([a + b for a, b in zip(self.data, other.data)], self.shape)

    def __sub__(self, other):
        self._check(other)
        return Tensor([a - b for a, b in zip(self.data, other.data)], self.shape)

    def scale(self, factor):
        return Tensor([factor * v for v in self.data], self.shape)

    def add_(self, other):
        self._check(other)
        for i, v in enumerate(other.data):
            self.data[i] += v
        return self

    def matmul(self, other):
        if len(self.shape) != 2 or len(other.shape) != 2:
            raise ValueError("matmul requires 2-D tensors")
        m, k = self.shape
        k2, n = other.shape
        if k != k2:
            raise ValueError("inner dimensions differ")
        out = [0.0] * (m * n)
        for i in range(m):
            for j in range(n):
                acc = 0.0
                for p in range(k):
                    acc += self.data[i * k + p] * other.data[p * n + j]
                out[i * n + j] = acc
        return Tensor(out, (m, n))

    def outer(self, other):
        if len(self.shape) != 1 or len(other.shape) != 1:
            raise ValueError("outer requires 1-D tensors")
        m, n = self.shape[0], other.shape[0]
        return Tensor([a * b for a in self.data for b in other.data], (m, n))

    def to_nested(self):
        if len(self.shape) == 2:
            r, c = self.shape
            return [self.data[i * c:(i + 1) * c] for i in range(r)]
        return list(self.data)

    def almost_equal(self, other, rel_tol=1e-9, abs_tol=1e-12):
        return self.shape == other.shape and all(
            math.isclose(a, b, rel_tol=rel_tol, abs_tol=abs_tol)
            for a, b in zip(self.data, other.data))

    def max_abs_diff(self, other):
        self._check(other)
        return max((abs(a - b) for a, b in zip(self.data, other.data)),
                   default=0.0)

    def to_json(self):
        return {"shape": list(self.shape), "data": list(self.data)}

    @classmethod
    def from_json(cls, payload):
        return cls(payload["data"], payload["shape"])

    def __repr__(self):
        return f"Tensor(shape={self.shape}, data={self.data!r})"
