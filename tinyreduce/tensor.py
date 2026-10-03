"""Minimal CPU tensor: flat buffer + shape + strides + optional mask.

Standard library only; no autodiff / tensor frameworks. Views (transpose /
strided slices) share the underlying buffer, which also gives us aliasing
for gradient-accumulation tests.
"""
from __future__ import annotations

from itertools import product


def default_strides(shape):
    """C-contiguous (row-major) strides, in elements."""
    acc = 1
    out = []
    for dim in reversed(shape):
        out.append(acc)
        acc *= dim
    return tuple(reversed(out))


def iter_indices_for(shape):
    """Yield every multi-index of ``shape`` in row-major order."""
    return product(*(range(d) for d in shape))


def _infer_shape(nested):
    if isinstance(nested, (list, tuple)):
        sub = None
        for item in nested:
            s = _infer_shape(item)
            if sub is None:
                sub = s
            elif s != sub:
                raise ValueError("ragged nested sequence")
        return (len(nested),) + (sub if sub is not None else ())
    return ()


def _flatten(nested, out):
    if isinstance(nested, (list, tuple)):
        for item in nested:
            _flatten(item, out)
    else:
        out.append(nested)


class Tensor:
    """A strided view over a shared flat buffer of floats.

    ``mask`` (optional) is aligned with the *buffer*, not the view:
    ``mask[pos] is False`` excludes the element at buffer position ``pos``
    from every reduction, as if it were absent.
    """

    __slots__ = ("data", "shape", "strides", "offset", "mask")

    def __init__(self, data, shape, strides=None, offset=0, mask=None):
        shape = tuple(int(d) for d in shape)
        if any(d < 0 for d in shape):
            raise ValueError(f"negative dimension in shape {shape}")
        strides = default_strides(shape) if strides is None else tuple(strides)
        if len(strides) != len(shape):
            raise ValueError("strides/shape rank mismatch")
        self.data = data
        self.shape = shape
        self.strides = strides
        self.offset = int(offset)
        self.mask = mask

    @classmethod
    def from_nested(cls, nested, mask_nested=None):
        shape = _infer_shape(nested)
        data = []
        _flatten(nested, data)
        mask = None
        if mask_nested is not None:
            if _infer_shape(mask_nested) != shape:
                raise ValueError("mask shape mismatch")
            mask = []
            _flatten(mask_nested, mask)
        return cls(data, shape, mask=mask)

    @property
    def ndim(self):
        return len(self.shape)

    @property
    def numel(self):
        n = 1
        for d in self.shape:
            n *= d
        return n

    def iter_indices(self):
        return iter_indices_for(self.shape)

    def flat_pos(self, index):
        pos = self.offset
        for i, s in zip(index, self.strides):
            pos += i * s
        return pos

    def included_pos(self, pos):
        return self.mask is None or self.mask[pos]

    def get(self, index=()):
        return self.data[self.flat_pos(index)]

    def set(self, index, value):
        self.data[self.flat_pos(index)] = value

    def logical_values(self):
        """Flat list of values in row-major logical order."""
        return [self.data[self.flat_pos(idx)] for idx in self.iter_indices()]

    def transpose(self, perm):
        perm = tuple(int(p) for p in perm)
        if sorted(perm) != list(range(self.ndim)):
            raise ValueError(f"bad permutation {perm}")
        return Tensor(self.data,
                      tuple(self.shape[p] for p in perm),
                      tuple(self.strides[p] for p in perm),
                      self.offset, self.mask)

    def slice_axis(self, axis, start=None, stop=None, step=None):
        """Strided slice along one axis; returns a non-contiguous view."""
        axis = int(axis)
        if axis < 0:
            axis += self.ndim
        if not 0 <= axis < self.ndim:
            raise ValueError(f"axis {axis} out of range for ndim {self.ndim}")
        r = range(self.shape[axis])[slice(start, stop, step)]
        shape = list(self.shape)
        strides = list(self.strides)
        shape[axis] = len(r)
        strides[axis] *= r.step
        offset = self.offset + (r.start * self.strides[axis] if len(r) else 0)
        return Tensor(self.data, tuple(shape), tuple(strides), offset, self.mask)

    def to_contiguous(self):
        data = []
        mask = [] if self.mask is not None else None
        for idx in self.iter_indices():
            pos = self.flat_pos(idx)
            data.append(self.data[pos])
            if mask is not None:
                mask.append(self.mask[pos])
        return Tensor(data, self.shape, mask=mask)

    def __eq__(self, other):
        return (isinstance(other, Tensor)
                and self.shape == other.shape
                and self.logical_values() == other.logical_values())

    def __repr__(self):
        return f"Tensor(shape={self.shape}, values={self.logical_values()})"
