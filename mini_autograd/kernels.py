"""Pure CPU kernels over flat float lists.

No differentiation lives here; these are the small tensor math helpers used by
both the forward operators and the independent finite-difference checker.
Shapes are tuples; broadcasting is supported for scalars (shape ``()``) and
identically shaped tensors only, which is everything the engine needs.
"""

from __future__ import annotations


def numel(shape):
    n = 1
    for d in shape:
        n *= d
    return n


def k_add(a, b):
    return _bin(a, b, lambda x, y: x + y)


def k_sub(a, b):
    return _bin(a, b, lambda x, y: x - y)


def k_mul(a, b):
    return _bin(a, b, lambda x, y: x * y)


def k_div(a, b):
    return _bin(a, b, lambda x, y: x / y)


def _bin(a, b, op):
    if len(a) == 1 and len(b) > 1:
        s = a[0]
        return [op(s, y) for y in b]
    if len(b) == 1 and len(a) > 1:
        s = b[0]
        return [op(x, s) for x in a]
    return [op(x, y) for x, y in zip(a, b)]


def k_neg(a):
    return [-x for x in a]


def k_pow_scalar(a, p):
    return [x ** p for x in a]


def k_relu(a):
    return [x if x > 0.0 else 0.0 for x in a]


def k_exp(a):
    import math

    return [math.exp(x) for x in a]


def k_log(a):
    import math

    return [math.log(x) for x in a]


def k_sum(a):
    return [sum(a)]


def k_mean(a):
    return [sum(a) / len(a)]


def k_matmul(a, b, m, k, n):
    """(m,k) x (k,n) -> (m,n)."""
    out = [0.0] * (m * n)
    for i in range(m):
        for t in range(n):
            s = 0.0
            for j in range(k):
                s += a[i * k + j] * b[j * n + t]
            out[i * n + t] = s
    return out


def k_transpose2(a, m, n):
    """(m,n) -> (n,m)."""
    out = [0.0] * (m * n)
    for i in range(m):
        for j in range(n):
            out[j * m + i] = a[i * n + j]
    return out


def k_dropout(a, mask, scale):
    return [v * m * scale for v, m in zip(a, mask)]


def broadcast_shape(sa, sb):
    if sa == sb:
        return sa
    if sa == ():
        return sb
    if sb == ():
        return sa
    raise ValueError(f"unsupported broadcast between shapes {sa} and {sb}")


def unbroadcast(grad, shape, target_shape):
    """Reduce a gradient of ``shape`` back down to ``target_shape`` (scalars)."""
    if shape == target_shape:
        return grad
    if target_shape == ():
        return [sum(grad)]
    raise ValueError("only scalar unbroadcast is implemented")
