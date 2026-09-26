"""A minimal CPU tensor: flat list of floats plus a shape tuple.

Deliberately tiny: no autodiff engine, no external tensor library.  Only
the operations needed by the layers and tests in this package are
provided.  Gradients are computed by explicit backward functions in
``keyrng.layers``.
"""

from __future__ import annotations

import math


def numel(shape: tuple[int, ...]) -> int:
    n = 1
    for dim in shape:
        if dim < 0:
            raise ValueError(f"negative dimension in shape {shape}")
        n *= dim
    return n


class Tensor:
    """Dense row-major tensor of Python floats."""

    __slots__ = ("data", "shape")

    def __init__(self, data, shape):
        self.shape = tuple(shape)
        self.data = [float(v) for v in data]
        if len(self.data) != numel(self.shape):
            raise ValueError(
                f"data length {len(self.data)} does not match shape {self.shape}"
            )

    @property
    def size(self) -> int:
        return len(self.data)

    @classmethod
    def zeros(cls, shape) -> "Tensor":
        return cls([0.0] * numel(tuple(shape)), tuple(shape))

    @classmethod
    def full(cls, shape, value: float) -> "Tensor":
        return cls([float(value)] * numel(tuple(shape)), tuple(shape))

    def map(self, fn) -> "Tensor":
        return Tensor([fn(v) for v in self.data], self.shape)

    def zip_with(self, other: "Tensor", fn) -> "Tensor":
        if self.shape != other.shape:
            raise ValueError(f"shape mismatch: {self.shape} vs {other.shape}")
        return Tensor([fn(a, b) for a, b in zip(self.data, other.data)], self.shape)

    def _binary(self, other, fn) -> "Tensor":
        if isinstance(other, Tensor):
            return self.zip_with(other, fn)
        return self.map(lambda a: fn(a, float(other)))

    def __add__(self, other) -> "Tensor":
        return self._binary(other, lambda a, b: a + b)

    def __sub__(self, other) -> "Tensor":
        return self._binary(other, lambda a, b: a - b)

    def __mul__(self, other) -> "Tensor":
        return self._binary(other, lambda a, b: a * b)

    def __truediv__(self, other) -> "Tensor":
        return self._binary(other, lambda a, b: a / b)

    __radd__ = __add__
    __rmul__ = __mul__

    def sum(self) -> float:
        return math.fsum(self.data)

    def mean(self) -> float:
        if not self.data:
            raise ValueError("mean of an empty tensor")
        return math.fsum(self.data) / len(self.data)

    def exact_equal(self, other: "Tensor") -> bool:
        return (
            isinstance(other, Tensor)
            and self.shape == other.shape
            and self.data == other.data
        )

    def allclose(self, other: "Tensor", tol: float = 1e-12) -> bool:
        return (
            isinstance(other, Tensor)
            and self.shape == other.shape
            and all(abs(a - b) <= tol for a, b in zip(self.data, other.data))
        )

    def __eq__(self, other) -> bool:
        return self.exact_equal(other)

    def __repr__(self) -> str:
        preview = ", ".join(f"{v:.6g}" for v in self.data[:6])
        if len(self.data) > 6:
            preview += ", ..."
        return f"Tensor(shape={self.shape}, data=[{preview}])"
