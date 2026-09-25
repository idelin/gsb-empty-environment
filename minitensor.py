"""Tiny pure-Python vector helpers.

Deliberately implemented on top of plain lists of floats: no tensor
libraries, no autodiff. Vectors are ``list[float]`` of equal length.
"""


def vec_zeros(dim):
    return [0.0] * dim


def vec_add_into(dst, src):
    """dst += src, elementwise, in place."""
    if len(dst) != len(src):
        raise ValueError("dimension mismatch")
    for j in range(len(dst)):
        dst[j] += src[j]


def vec_copy(v):
    return list(v)


def vec_close(a, b, tol=1e-9):
    return len(a) == len(b) and all(abs(x - y) <= tol for x, y in zip(a, b))
