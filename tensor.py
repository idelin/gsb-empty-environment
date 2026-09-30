"""Minimal dense 1-D tensor type implemented with the standard library only.

No autodiff, no third-party tensor libraries: a Tensor is a flat list of
Python floats plus the handful of operations the optimizer needs.
"""

from __future__ import annotations

import math
from typing import Iterable


class Tensor:
    """A 1-D dense tensor backed by a plain list of floats."""

    __slots__ = ("data",)

    def __init__(self, data: Iterable[float]):
        self.data = [float(x) for x in data]

    @classmethod
    def zeros(cls, size: int) -> "Tensor":
        return cls([0.0] * size)

    def copy(self) -> "Tensor":
        return Tensor(self.data)

    def is_finite(self) -> bool:
        return all(math.isfinite(x) for x in self.data)

    def __len__(self) -> int:
        return len(self.data)

    def __eq__(self, other: object) -> bool:
        return isinstance(other, Tensor) and self.data == other.data

    def __repr__(self) -> str:
        return f"Tensor({self.data!r})"


def add(a: Tensor, b: Tensor) -> Tensor:
    _check_same_size(a, b)
    return Tensor([x + y for x, y in zip(a.data, b.data)])


def _check_same_size(a: Tensor, b: Tensor) -> None:
    if len(a) != len(b):
        raise ValueError(f"tensor size mismatch: {len(a)} != {len(b)}")
