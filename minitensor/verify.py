"""Independent verification helpers.

These deliberately do *not* share implementation with :mod:`minitensor.tensor`
so tests can cross-check the library against a second, independent source of
truth (address enumeration, view materialization, matrix multiply).
"""
from __future__ import annotations


def enumerate_addresses(shape, strides, offset):
    """Recursively enumerate reachable element addresses in C order.

    Independent of ``Tensor.addresses`` (recursion over axes instead of the
    odometer loop), used to cross-check iteration order and bounds.
    """
    shape = tuple(shape)
    strides = tuple(strides)
    if any(n == 0 for n in shape):
        return []
    result = []

    def walk(axis, base):
        if axis == len(shape):
            result.append(base)
            return
        step = strides[axis]
        for i in range(shape[axis]):
            walk(axis + 1, base + i * step)

    walk(0, offset)
    return result


def reference_view(flat, shape, strides, offset):
    """Materialize a strided view of ``flat`` as nested lists, independently."""
    shape = tuple(shape)
    strides = tuple(strides)

    def build(axis, base):
        if axis == len(shape):
            return flat[base]
        return [build(axis + 1, base + i * strides[axis]) for i in range(shape[axis])]

    return build(0, offset)


def matmul_tensor(a, b):
    """Matrix multiply two rank-2 Tensors via their public getitem API."""
    if a.ndim != 2 or b.ndim != 2:
        raise ValueError("matmul expects rank-2 tensors")
    m, k = a.shape
    k2, n = b.shape
    if k != k2:
        raise ValueError("inner dimensions differ")
    out = []
    for i in range(m):
        row = []
        for j in range(n):
            acc = 0.0
            for p in range(k):
                acc += a[i, p] * b[p, j]
            row.append(acc)
        out.append(row)
    return out


def reference_matmul(a_rows, b_rows):
    """Pure-Python matmul over nested lists (independent reference)."""
    if not a_rows or not b_rows:
        raise ValueError("empty operand")
    inner = len(b_rows)
    width = len(b_rows[0])
    if any(len(r) != inner for r in a_rows):
        raise ValueError("mismatched operands")
    if any(len(r) != width for r in b_rows):
        raise ValueError("ragged operand")
    cols = list(zip(*b_rows))
    return [
        [sum(x * y for x, y in zip(row, col)) for col in cols]
        for row in a_rows
    ]
