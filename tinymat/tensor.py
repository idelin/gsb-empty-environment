"""Minimal strided tensor over a shared flat buffer (Python stdlib only).

A Tensor is a *view*: ``data`` is a flat list of floats that may be shared
with other tensors, while ``shape`` / ``strides`` / ``offset`` describe how
logical indices map to physical storage cells.  Broadcasting is expressed
with stride-0 axes, so a broadcast view never copies the underlying buffer.
"""

from itertools import product


def default_strides(shape):
    """Row-major (C-order) strides, in elements."""
    strides = []
    acc = 1
    for size in reversed(shape):
        strides.append(acc)
        acc *= size
    return tuple(reversed(strides))


def iter_indices(shape):
    """Yield every logical index tuple of a shape (once for 0-d)."""
    if not shape:
        yield ()
        return
    yield from product(*(range(s) for s in shape))


class Tensor:
    __slots__ = ("data", "shape", "strides", "offset")

    def __init__(self, data, shape, strides=None, offset=0):
        self.data = data
        self.shape = tuple(int(s) for s in shape)
        if strides is None:
            strides = default_strides(self.shape)
        self.strides = tuple(int(s) for s in strides)
        self.offset = int(offset)

    # ------------------------------------------------------------------
    # constructors
    # ------------------------------------------------------------------
    @classmethod
    def zeros(cls, shape):
        shape = tuple(shape)
        n = 1
        for s in shape:
            n *= s
        return cls([0.0] * n, shape)

    @classmethod
    def from_nested(cls, nested):
        """Build a contiguous tensor from (possibly nested) lists."""
        if not isinstance(nested, (list, tuple)):
            return cls([float(nested)], ())
        subs = [cls.from_nested(x) for x in nested]
        shape = (len(nested),)
        if subs:
            sub_shape = subs[0].shape
            for s in subs:
                if s.shape != sub_shape:
                    raise ValueError("ragged nested lists")
            shape = shape + sub_shape
        flat = []
        for s in subs:
            flat.extend(s.data)
        return cls(flat, shape)

    # ------------------------------------------------------------------
    # basic properties / element access (stride-aware)
    # ------------------------------------------------------------------
    @property
    def ndim(self):
        return len(self.shape)

    def physical_index(self, index):
        return self.offset + sum(i * s for i, s in zip(index, self.strides))

    def value_at(self, index):
        return self.data[self.physical_index(index)]

    def set_at(self, index, value):
        self.data[self.physical_index(index)] = value

    def physical_cells(self):
        """Sorted unique physical storage cells this view can touch."""
        return sorted({self.physical_index(idx) for idx in iter_indices(self.shape)})

    def to_nested(self):
        def build(dim, prefix):
            if dim == self.ndim:
                return self.value_at(tuple(prefix))
            return [build(dim + 1, prefix + [i]) for i in range(self.shape[dim])]
        return build(0, [])

    # ------------------------------------------------------------------
    # views (no copies)
    # ------------------------------------------------------------------
    def view(self, shape, strides, offset=None):
        return Tensor(self.data, shape, strides,
                      self.offset if offset is None else offset)

    def transpose(self, *axes):
        if not axes:
            axes = tuple(reversed(range(self.ndim)))
        if sorted(axes) != list(range(self.ndim)):
            raise ValueError("axes must be a permutation")
        return self.view(tuple(self.shape[i] for i in axes),
                         tuple(self.strides[i] for i in axes))

    def broadcast_to(self, shape):
        shape = tuple(int(s) for s in shape)
        if len(shape) < self.ndim:
            raise ValueError("cannot broadcast to fewer dims")
        pad = len(shape) - self.ndim
        padded_shape = (1,) * pad + self.shape
        padded_strides = (0,) * pad + self.strides
        out_strides = []
        for cur, want, st in zip(padded_shape, shape, padded_strides):
            if cur == want:
                out_strides.append(st)
            elif cur == 1:
                out_strides.append(0)  # broadcast: stride 0, no copy
            else:
                raise ValueError(f"cannot broadcast {self.shape} to {shape}")
        return self.view(shape, tuple(out_strides))

    def slice_dim(self, dim, start=None, stop=None, step=None):
        st, sp, stp = slice(start, stop, step).indices(self.shape[dim])
        length = len(range(st, sp, stp))
        shape = list(self.shape)
        strides = list(self.strides)
        offset = self.offset + st * strides[dim]
        shape[dim] = length
        strides[dim] *= stp
        return self.view(tuple(shape), tuple(strides), offset)

    def contiguous(self):
        """Materialize a compact copy (used for outputs, never to bypass
        broadcast/stride handling inside kernels)."""
        out = Tensor.zeros(self.shape)
        for idx in iter_indices(self.shape):
            out.set_at(idx, self.value_at(idx))
        return out

    def __repr__(self):
        return (f"Tensor(shape={self.shape}, strides={self.strides}, "
                f"offset={self.offset}, data={self.to_nested()!r})")
