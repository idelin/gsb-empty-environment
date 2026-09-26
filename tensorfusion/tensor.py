"""Minimal CPU tensor implemented from scratch (standard library only).

A Tensor is a view onto a shared Storage: flat list of floats plus
shape/strides/offset.  Views (transpose / narrow / broadcast_to) share the
same Storage, which lets the fuser detect aliasing precisely.  Storage
carries a version counter that is bumped by user-visible in-place mutation,
which the fuser uses as version-dependency evidence.
"""

from __future__ import annotations

import itertools


class Storage:
    __slots__ = ("data", "version", "sid")
    _next_sid = 0

    def __init__(self, data):
        self.data = [float(v) for v in data]
        self.version = 0
        self.sid = Storage._next_sid
        Storage._next_sid += 1


def numel(shape):
    n = 1
    for s in shape:
        n *= s
    return n


def default_strides(shape):
    acc = 1
    out = []
    for size in reversed(shape):
        out.append(acc)
        acc *= size
    return tuple(reversed(out))


def iter_indices(shape):
    if not shape:
        yield ()
        return
    yield from itertools.product(*(range(s) for s in shape))


def broadcast_shape(*shapes):
    result = []
    for dims in itertools.zip_longest(*(reversed(s) for s in shapes), fillvalue=1):
        target = max(dims)
        for d in dims:
            if d not in (1, target):
                raise ValueError(f"shapes not broadcastable: {shapes}")
        result.append(target)
    return tuple(reversed(result))


def align_strides(shape, strides, out_shape):
    """Strides of a (shape, strides) view re-expressed over out_shape.

    Broadcast dimensions get stride 0.  Raises ValueError if a non-trivial
    dimension does not match the output dimension.
    """
    if len(shape) > len(out_shape):
        raise ValueError(f"cannot align {shape} to {out_shape}")
    pad = len(out_shape) - len(shape)
    aligned = [0] * pad
    for s, st, o in zip(shape, strides, out_shape[pad:]):
        if s == o:
            aligned.append(st)
        elif s == 1:
            aligned.append(0)
        else:
            raise ValueError(f"cannot align {shape} to {out_shape}")
    return tuple(aligned)


def project_index(out_index, in_shape, aligned_strides):
    """Logical output index -> logical operand index under broadcasting.

    aligned_strides is the operand's stride vector aligned to the output
    rank; the result has the operand's own rank (padded dims dropped,
    broadcast dims zeroed).
    """
    pad = len(out_index) - len(in_shape)
    return tuple(0 if st == 0 else i
                 for i, st in zip(out_index[pad:], aligned_strides[pad:]))


class Tensor:
    __slots__ = ("storage", "shape", "strides", "offset", "name")

    def __init__(self, storage, shape, strides=None, offset=0, name=None):
        self.storage = storage
        self.shape = tuple(shape)
        self.strides = tuple(strides) if strides is not None else default_strides(self.shape)
        self.offset = offset
        self.name = name

    @classmethod
    def from_values(cls, values, shape=None, name=None):
        values = list(values)
        if shape is None:
            shape = (len(values),)
        shape = tuple(shape)
        if numel(shape) != len(values):
            raise ValueError(f"{len(values)} values do not fit shape {shape}")
        return cls(Storage(values), shape, name=name)

    @classmethod
    def full(cls, shape, value, name=None):
        shape = tuple(shape)
        return cls(Storage([value] * numel(shape)), shape, name=name)

    @property
    def size(self):
        return numel(self.shape)

    @property
    def version(self):
        return self.storage.version

    def indices(self):
        return iter_indices(self.shape)

    def flat_pos(self, index):
        return self.offset + sum(i * s for i, s in zip(index, self.strides))

    def get(self, index):
        return self.storage.data[self.flat_pos(index)]

    def _write(self, index, value):
        """Internal write used by interpreters; does not bump the version."""
        self.storage.data[self.flat_pos(index)] = float(value)

    def set(self, index, value):
        """User-visible in-place mutation: bumps the storage version."""
        self._write(index, value)
        self.storage.version += 1

    def to_flat(self):
        return [self.get(i) for i in self.indices()]

    def shares_storage(self, other):
        return self.storage.sid == other.storage.sid

    def transpose(self):
        return Tensor(self.storage, tuple(reversed(self.shape)),
                      tuple(reversed(self.strides)), self.offset, self.name)

    def narrow(self, dim, start, length):
        shape = list(self.shape)
        offset = self.offset + start * self.strides[dim]
        shape[dim] = length
        return Tensor(self.storage, tuple(shape), self.strides, offset, self.name)

    def broadcast_to(self, shape):
        shape = tuple(shape)
        return Tensor(self.storage, shape, align_strides(self.shape, self.strides, shape),
                      self.offset, self.name)

    def aligned_strides(self, out_shape):
        return align_strides(self.shape, self.strides, out_shape)

    def __repr__(self):
        return f"Tensor(name={self.name!r}, shape={self.shape}, data={self.to_flat()!r})"
