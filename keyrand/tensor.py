"""CPU 小规模张量：扁平 float 列表 + shape，仅标准库，无自动微分。"""
from __future__ import annotations

import math
from typing import Callable, Iterable


def _numel(shape: tuple[int, ...]) -> int:
    n = 1
    for d in shape:
        if d < 0:
            raise ValueError(f"非法维度: {shape}")
        n *= d
    return n


class Tensor:
    __slots__ = ("data", "shape")

    def __init__(self, data: Iterable[float], shape: tuple[int, ...]):
        flat = [float(v) for v in data]
        if len(flat) != _numel(tuple(shape)):
            raise ValueError(f"数据长度 {len(flat)} 与形状 {shape} 不匹配")
        self.data = flat
        self.shape = tuple(shape)

    # ---- 构造 ----
    @classmethod
    def zeros(cls, shape: tuple[int, ...]) -> "Tensor":
        return cls([0.0] * _numel(tuple(shape)), tuple(shape))

    @classmethod
    def full(cls, shape: tuple[int, ...], value: float) -> "Tensor":
        return cls([float(value)] * _numel(tuple(shape)), tuple(shape))

    # ---- 基本属性 ----
    @property
    def numel(self) -> int:
        return len(self.data)

    def to_list(self) -> list[float]:
        return list(self.data)

    def __eq__(self, other):
        return (
            isinstance(other, Tensor)
            and self.shape == other.shape
            and self.data == other.data
        )

    def __repr__(self):
        return f"Tensor(shape={self.shape}, data={self.data!r})"

    # ---- 逐元素运算（支持标量广播） ----
    def _binary(self, other, op: Callable[[float, float], float]) -> "Tensor":
        if isinstance(other, Tensor):
            if self.shape != other.shape:
                raise ValueError(f"形状不匹配: {self.shape} vs {other.shape}")
            return Tensor([op(a, b) for a, b in zip(self.data, other.data)], self.shape)
        scalar = float(other)
        return Tensor([op(a, scalar) for a in self.data], self.shape)

    def __add__(self, other):
        return self._binary(other, lambda a, b: a + b)

    __radd__ = __add__

    def __sub__(self, other):
        return self._binary(other, lambda a, b: a - b)

    def __rsub__(self, other):
        return self._binary(other, lambda a, b: b - a)

    def __mul__(self, other):
        return self._binary(other, lambda a, b: a * b)

    __rmul__ = __mul__

    def __truediv__(self, other):
        return self._binary(other, lambda a, b: a / b)

    def map(self, fn: Callable[[float], float]) -> "Tensor":
        return Tensor([fn(v) for v in self.data], self.shape)

    # ---- 归约 ----
    def sum(self) -> float:
        return math.fsum(self.data)

    def mean(self) -> float:
        if not self.data:
            raise ValueError("空张量无均值")
        return self.sum() / len(self.data)
