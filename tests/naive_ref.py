"""Fully independent reference: batched matmul on plain nested lists.

Shares no code with tinymat; broadcasting is done by index clamping and the
core is a literal triple loop.
"""

from itertools import product


def shape_of(nested):
    shape = []
    node = nested
    while isinstance(node, list):
        shape.append(len(node))
        node = node[0] if node else None
    return tuple(shape)


def _broadcast_batch(s1, s2):
    n = max(len(s1), len(s2))
    s1 = (1,) * (n - len(s1)) + s1
    s2 = (1,) * (n - len(s2)) + s2
    out = []
    for x, y in zip(s1, s2):
        assert x == y or x == 1 or y == 1, (s1, s2)
        out.append(max(x, y))
    return tuple(out)


def _get(nested, shape, index):
    node = nested
    for size, i in zip(shape, index):
        node = node[0 if size == 1 else i]
    return node


def _zeros(shape):
    if not shape:
        return 0.0
    return [_zeros(shape[1:]) for _ in range(shape[0])]


def _set(nested, index, value):
    node = nested
    for i in index[:-1]:
        node = node[i]
    node[index[-1]] = value


def _drop_axis(nested, shape, pos):
    """Remove a size-1 axis at position pos from a nested list."""
    if pos == 0:
        return nested[0]
    return [_drop_axis(x, shape[1:], pos - 1) for x in nested]


def naive_matmul(a, b, sa=None, sb=None):
    """Returns (nested_result, shape).  Handles vec promotion, broadcasting
    and k == 0 (result is zeros, n taken from b's padded shape).

    Explicit shapes may be supplied because nested lists lose the sizes of
    trailing empty dimensions (e.g. a (0, 3) tensor is just ``[]``)."""
    sa = tuple(sa) if sa is not None else shape_of(a)
    sb = tuple(sb) if sb is not None else shape_of(b)
    a_vec = len(sa) == 1
    b_vec = len(sb) == 1
    if a_vec:
        a = [a]
        sa = (1,) + sa
    if b_vec:
        b = [[x] for x in b]
        sb = sb + (1,)
    m, k = sa[-2], sa[-1]
    k2, n = sb[-2], sb[-1]
    assert k == k2, (sa, sb)
    batch = _broadcast_batch(sa[:-2], sb[:-2])
    nb = len(batch)
    pa = (1,) * (nb - (len(sa) - 2)) + sa[:-2]
    pb = (1,) * (nb - (len(sb) - 2)) + sb[:-2]
    out_shape = batch + (m, n)
    out = _zeros(out_shape)
    for bidx in product(*(range(s) for s in batch)) if batch else [()]:
        ai = tuple(0 if d == 1 else i for d, i in zip(pa, bidx))
        bi = tuple(0 if d == 1 else i for d, i in zip(pb, bidx))
        # keep only the operand's own trailing batch dims (left-padded
        # dims are size-1 placeholders, not real axes of the operand)
        ai = ai[nb - (len(sa) - 2):]
        bi = bi[nb - (len(sb) - 2):]
        for i in range(m):
            for j in range(n):
                acc = 0.0
                for kk in range(k):  # the independent triple loop
                    acc += (_get(a, sa, ai + (i, kk))
                            * _get(b, sb, bi + (kk, j)))
                _set(out, bidx + (i, j), acc)
    if a_vec:
        out = _drop_axis(out, out_shape, len(out_shape) - 2)
        out_shape = out_shape[:-2] + out_shape[-1:]
    if b_vec:
        out = _drop_axis(out, out_shape, len(out_shape) - 1)
        out_shape = out_shape[:-1]
    return out, out_shape


def flatten(nested):
    if isinstance(nested, list):
        out = []
        for x in nested:
            out.extend(flatten(x))
        return out
    return [nested]
