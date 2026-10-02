"""Tiny pure-Python dense tensor library (Python 3.11 stdlib only).

Tensors are stored as a flat list of floats plus a shape tuple.  Only small
CPU tensors are supported; indexing, broadcasting and linear algebra are
implemented by hand (no numpy / autodiff dependencies).
"""
from __future__ import annotations

import math
import random
from typing import Callable, Sequence


def numel(shape: Sequence[int]) -> int:
    n = 1
    for s in shape:
        n *= s
    return n


def strides_of(shape: Sequence[int]) -> tuple[int, ...]:
    strides = []
    acc = 1
    for s in reversed(shape):
        strides.append(acc)
        acc *= s
    strides.reverse()
    return tuple(strides)


def _unravel(flat: int, shape: tuple[int, ...]) -> tuple[int, ...]:
    return tuple(flat // stride % dim for stride, dim in zip(strides_of(shape), shape))


def _ravel(idx: Sequence[int], shape: tuple[int, ...]) -> int:
    return sum(i * s for i, s in zip(idx, strides_of(shape)))


class Tensor:
    """Flat-storage dense tensor of floats."""

    __slots__ = ("data", "shape")

    def __init__(self, data, shape):
        self.shape = tuple(int(s) for s in shape)
        self.data = [float(v) for v in data]
        if len(self.data) != numel(self.shape):
            raise ValueError(f"data length {len(self.data)} does not match shape {self.shape}")

    @staticmethod
    def zeros(shape) -> "Tensor":
        shape = tuple(shape)
        return Tensor([0.0] * numel(shape), shape)

    @staticmethod
    def ones(shape) -> "Tensor":
        shape = tuple(shape)
        return Tensor([1.0] * numel(shape), shape)

    @staticmethod
    def scalar(value: float) -> "Tensor":
        return Tensor([value], ())

    @staticmethod
    def randn(shape, seed: int = 0) -> "Tensor":
        shape = tuple(shape)
        rng = random.Random(seed)
        return Tensor([rng.gauss(0.0, 1.0) for _ in range(numel(shape))], shape)

    @staticmethod
    def from_nested(nested) -> "Tensor":
        shape: list[int] = []
        flat: list[float] = []

        def walk(node, depth):
            if isinstance(node, (list, tuple)):
                if depth == len(shape):
                    shape.append(len(node))
                elif shape[depth] != len(node):
                    raise ValueError("ragged nested list")
                for child in node:
                    walk(child, depth + 1)
            else:
                if depth != len(shape):
                    raise ValueError("ragged nested list")
                flat.append(float(node))

        walk(nested, 0)
        return Tensor(flat, tuple(shape))

    def to_nested(self):
        def build(offset, shape):
            if not shape:
                return self.data[offset]
            step = numel(shape[1:])
            return [build(offset + i * step, shape[1:]) for i in range(shape[0])]

        return build(0, self.shape)

    def item(self) -> float:
        if self.shape != ():
            raise ValueError("item() requires a scalar tensor")
        return self.data[0]

    def __repr__(self) -> str:
        return f"Tensor(shape={self.shape}, data={self.to_nested()!r})"


def zeros_like(t: Tensor) -> Tensor:
    return Tensor.zeros(t.shape)


def ones_like(t: Tensor) -> Tensor:
    return Tensor.ones(t.shape)


def broadcast_shape(a, b) -> tuple[int, ...]:
    la, lb = len(a), len(b)
    out = []
    for i in range(max(la, lb)):
        da = a[la - 1 - i] if i < la else 1
        db = b[lb - 1 - i] if i < lb else 1
        if da != db and da != 1 and db != 1:
            raise ValueError(f"shapes {tuple(a)} and {tuple(b)} are not broadcastable")
        out.append(max(da, db))
    return tuple(reversed(out))


def broadcast_to(t: Tensor, shape) -> Tensor:
    shape = tuple(shape)
    if t.shape == shape:
        return Tensor(list(t.data), shape)
    if len(t.shape) > len(shape) or broadcast_shape(t.shape, shape) != shape:
        raise ValueError(f"cannot broadcast {t.shape} to {shape}")
    offset = len(shape) - len(t.shape)
    strides = strides_of(t.shape)
    out = []
    for flat in range(numel(shape)):
        idx = _unravel(flat, shape)
        src = 0
        for d in range(len(t.shape)):
            i = idx[offset + d]
            if t.shape[d] == 1:
                i = 0
            src += i * strides[d]
        out.append(t.data[src])
    return Tensor(out, shape)


def binary_op(a: Tensor, b: Tensor, fn: Callable[[float, float], float]) -> Tensor:
    shape = broadcast_shape(a.shape, b.shape)
    aa = broadcast_to(a, shape)
    bb = broadcast_to(b, shape)
    return Tensor([fn(x, y) for x, y in zip(aa.data, bb.data)], shape)


def unary_op(a: Tensor, fn: Callable[[float, float]]) -> Tensor:
    return Tensor([fn(x) for x in a.data], a.shape)


def add(a, b):
    return binary_op(a, b, lambda x, y: x + y)


def sub(a, b):
    return binary_op(a, b, lambda x, y: x - y)


def mul(a, b):
    return binary_op(a, b, lambda x, y: x * y)


def div(a, b):
    return binary_op(a, b, lambda x, y: x / y)


def neg(a):
    return unary_op(a, lambda x: -x)


def exp(a):
    return unary_op(a, math.exp)


def tanh(a):
    return unary_op(a, math.tanh)


def log(a):
    return unary_op(a, math.log)


def square(a):
    return unary_op(a, lambda x: x * x)


def reduce_sum(t: Tensor, axis: int, keepdims: bool = False) -> Tensor:
    axis %= len(t.shape)
    if keepdims:
        out_shape = t.shape[:axis] + (1,) + t.shape[axis + 1:]
    else:
        out_shape = t.shape[:axis] + t.shape[axis + 1:]
    out = Tensor.zeros(out_shape)
    for flat, value in enumerate(t.data):
        idx = list(_unravel(flat, t.shape))
        if keepdims:
            idx[axis] = 0
        else:
            del idx[axis]
        out.data[_ravel(idx, out_shape)] += value
    return out


def reduce_mean(t: Tensor, axis: int, keepdims: bool = False) -> Tensor:
    axis %= len(t.shape)
    summed = reduce_sum(t, axis, keepdims)
    size = t.shape[axis]
    return unary_op(summed, lambda x: x / size)


def transpose(t: Tensor, perm) -> Tensor:
    perm = tuple(p % len(t.shape) for p in perm)
    if sorted(perm) != list(range(len(t.shape))):
        raise ValueError(f"invalid permutation {perm} for shape {t.shape}")
    out_shape = tuple(t.shape[p] for p in perm)
    out = Tensor.zeros(out_shape)
    for flat in range(numel(out_shape)):
        idx = _unravel(flat, out_shape)
        src = [0] * len(perm)
        for new_pos, p in enumerate(perm):
            src[p] = idx[new_pos]
        out.data[flat] = t.data[_ravel(src, t.shape)]
    return out


def transpose_last2(t: Tensor) -> Tensor:
    perm = list(range(len(t.shape)))
    perm[-1], perm[-2] = perm[-2], perm[-1]
    return transpose(t, perm)


def reshape(t: Tensor, shape) -> Tensor:
    shape = tuple(int(s) for s in shape)
    if numel(shape) != numel(t.shape):
        raise ValueError(f"cannot reshape {t.shape} to {shape}")
    return Tensor(list(t.data), shape)


def moveaxis(t: Tensor, src: int, dst: int) -> Tensor:
    n = len(t.shape)
    src %= n
    dst %= n
    perm = list(range(n))
    axis = perm.pop(src)
    perm.insert(dst, axis)
    return transpose(t, perm)


def matmul(a: Tensor, b: Tensor) -> Tensor:
    """Batched matmul: ranks >= 2, leading (batch) dims broadcast."""
    if len(a.shape) < 2 or len(b.shape) < 2:
        raise ValueError("matmul requires rank >= 2")
    m, k = a.shape[-2], a.shape[-1]
    k2, n = b.shape[-2], b.shape[-1]
    if k != k2:
        raise ValueError(f"matmul inner dims differ: {a.shape} vs {b.shape}")
    batch = broadcast_shape(a.shape[:-2], b.shape[:-2])
    aa = broadcast_to(a, batch + (m, k))
    bb = broadcast_to(b, batch + (k, n))
    out = Tensor.zeros(batch + (m, n))
    a_st = strides_of(aa.shape)
    b_st = strides_of(bb.shape)
    o_st = strides_of(out.shape)
    for bi in range(numel(batch)):
        bidx = _unravel(bi, batch)
        a_off = sum(i * s for i, s in zip(bidx, a_st))
        b_off = sum(i * s for i, s in zip(bidx, b_st))
        o_off = sum(i * s for i, s in zip(bidx, o_st))
        for i in range(m):
            for j in range(n):
                acc = 0.0
                for kk in range(k):
                    acc += (aa.data[a_off + i * a_st[-2] + kk * a_st[-1]]
                            * bb.data[b_off + kk * b_st[-2] + j * b_st[-1]])
                out.data[o_off + i * o_st[-2] + j * o_st[-1]] = acc
    return out


def stack(tensors, axis: int = 0) -> Tensor:
    tensors = list(tensors)
    if not tensors:
        raise ValueError("stack requires at least one tensor")
    base = tensors[0].shape
    for t in tensors:
        if t.shape != base:
            raise ValueError(f"stack: shape mismatch {t.shape} vs {base}")
    axis %= (len(base) + 1)
    out_shape = base[:axis] + (len(tensors),) + base[axis:]
    out = Tensor.zeros(out_shape)
    for which, t in enumerate(tensors):
        for flat, value in enumerate(t.data):
            idx = list(_unravel(flat, base))
            idx.insert(axis, which)
            out.data[_ravel(idx, out_shape)] = value
    return out


def slice_along(t: Tensor, axis: int, index: int) -> Tensor:
    """Take `t[index]` along `axis`, removing that axis."""
    axis %= len(t.shape)
    if not 0 <= index < t.shape[axis]:
        raise IndexError(index)
    out_shape = t.shape[:axis] + t.shape[axis + 1:]
    out = Tensor.zeros(out_shape)
    for flat in range(numel(out_shape)):
        idx = list(_unravel(flat, out_shape))
        idx.insert(axis, index)
        out.data[flat] = t.data[_ravel(idx, t.shape)]
    return out


def unbroadcast(g: Tensor, shape) -> Tensor:
    """Sum `g` down to `shape` (inverse of broadcasting)."""
    shape = tuple(shape)
    t = g
    while len(t.shape) > len(shape):
        t = reduce_sum(t, 0)
    for d in range(len(shape)):
        if shape[d] == 1 and t.shape[d] != 1:
            t = reduce_sum(t, d, keepdims=True)
    if t.shape != shape:
        t = reshape(t, shape)
    return t


def sum_all(t: Tensor) -> float:
    return float(sum(t.data))


def max_abs_diff(a: Tensor, b: Tensor) -> float:
    if a.shape != b.shape:
        raise ValueError(f"shape mismatch {a.shape} vs {b.shape}")
    return max((abs(x - y) for x, y in zip(a.data, b.data)), default=0.0)
