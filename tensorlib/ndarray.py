"""Minimal N-dimensional array with strided views, pure Python.

An NDArray is a (storage, shape, strides, offset) quadruple.  Views such as
``transpose``/``permute``/``broadcast_to`` share the same ``_Storage`` object,
so in-place writes through any view are visible in all of them and bump a
single shared version counter (used by the autograd engine for version
checks).
"""


class ShapeError(Exception):
    """Raised when shapes are not broadcast-compatible or otherwise invalid."""


def _strides_for(shape):
    acc = 1
    out = []
    for s in reversed(shape):
        out.append(acc)
        acc *= s
    return tuple(reversed(out))


def _numel(shape):
    n = 1
    for s in shape:
        n *= s
    return n


def _iter_indices(shape):
    """Yield every index of ``shape`` in C order.  Handles scalars and
    zero-sized dimensions (which yield nothing)."""
    if len(shape) == 0:
        yield ()
        return
    if 0 in shape:
        return
    idx = [0] * len(shape)
    while True:
        yield tuple(idx)
        ax = len(shape) - 1
        while ax >= 0:
            idx[ax] += 1
            if idx[ax] < shape[ax]:
                break
            idx[ax] = 0
            ax -= 1
        if ax < 0:
            return


def broadcast_shape(*shapes):
    ndim = max((len(s) for s in shapes), default=0)
    out = []
    for ax in range(ndim):
        dim = 1
        for s in shapes:
            i = len(s) - ndim + ax
            if i < 0:
                continue
            d = s[i]
            if d == 1:
                continue
            if dim == 1:
                dim = d
            elif dim != d:
                raise ShapeError(
                    "shapes %s are not broadcast-compatible" % (shapes,))
        out.append(dim)
    return tuple(out)


class _Storage:
    __slots__ = ("data", "version")

    def __init__(self, data):
        self.data = data
        self.version = 0


class NDArray:
    __slots__ = ("storage", "shape", "strides", "offset")

    def __init__(self, storage, shape, strides=None, offset=0):
        if not isinstance(storage, _Storage):
            storage = _Storage(list(storage))
        self.storage = storage
        self.shape = tuple(int(s) for s in shape)
        if strides is None:
            strides = _strides_for(self.shape)
        self.strides = tuple(strides)
        self.offset = offset

    # ----------------------------------------------------------- constructors
    @classmethod
    def zeros(cls, shape):
        return cls([0.0] * _numel(shape), shape)

    @classmethod
    def full(cls, shape, value):
        return cls([float(value)] * _numel(shape), shape)

    @classmethod
    def from_nested(cls, obj):
        if isinstance(obj, NDArray):
            return obj
        if isinstance(obj, (int, float)):
            return cls([float(obj)], ())
        shape = []

        def walk(o, depth):
            if isinstance(o, (list, tuple)):
                if depth == len(shape):
                    shape.append(len(o))
                elif shape[depth] != len(o):
                    raise ShapeError("ragged nested sequence")
                for it in o:
                    walk(it, depth + 1)

        walk(obj, 0)
        flat = []

        def flatwalk(o):
            if isinstance(o, (list, tuple)):
                for it in o:
                    flatwalk(it)
            else:
                flat.append(float(o))

        flatwalk(obj)
        return cls(flat, tuple(shape))

    # ------------------------------------------------------------- properties
    @property
    def ndim(self):
        return len(self.shape)

    @property
    def version(self):
        return self.storage.version

    # ---------------------------------------------------------- element access
    def _flat_index(self, idx):
        off = self.offset
        for i, s in zip(idx, self.strides):
            off += i * s
        return off

    def get(self, idx):
        return self.storage.data[self._flat_index(idx)]

    def set(self, idx, value):
        self.storage.data[self._flat_index(idx)] = value

    def to_nested(self):
        if self.ndim == 0:
            return self.get(())

        def build(prefix, ax):
            if ax == self.ndim:
                return self.get(prefix)
            return [build(prefix + (i,), ax + 1) for i in range(self.shape[ax])]

        return build((), 0)

    # ------------------------------------------------------------------- views
    def broadcast_to(self, shape):
        shape = tuple(shape)
        nd = len(shape) - self.ndim
        if nd < 0:
            raise ShapeError("cannot broadcast %s to %s" % (self.shape, shape))
        padded_shape = (1,) * nd + self.shape
        padded_strides = (0,) * nd + self.strides
        out_strides = []
        for want, have, st in zip(shape, padded_shape, padded_strides):
            if have == want:
                out_strides.append(st)
            elif have == 1:
                out_strides.append(0)
            else:
                raise ShapeError(
                    "cannot broadcast %s to %s" % (self.shape, shape))
        return NDArray(self.storage, shape, tuple(out_strides), self.offset)

    def permute(self, perm):
        perm = tuple(p % self.ndim for p in perm)
        if sorted(perm) != list(range(self.ndim)):
            raise ShapeError("invalid permutation %s" % (perm,))
        return NDArray(self.storage,
                       tuple(self.shape[i] for i in perm),
                       tuple(self.strides[i] for i in perm),
                       self.offset)

    def transpose(self):
        return self.permute(tuple(reversed(range(self.ndim))))

    def unsqueeze(self, axes):
        shape = list(self.shape)
        strides = list(self.strides)
        for ax in sorted(axes):
            shape.insert(ax, 1)
            strides.insert(ax, 0)
        return NDArray(self.storage, shape, strides, self.offset)

    def squeeze(self, axes):
        axes = sorted((a % self.ndim for a in axes), reverse=True)
        shape = list(self.shape)
        strides = list(self.strides)
        for ax in axes:
            if shape[ax] != 1:
                raise ShapeError("cannot squeeze dim %d of size %d"
                                 % (ax, shape[ax]))
            del shape[ax]
            del strides[ax]
        return NDArray(self.storage, shape, strides, self.offset)

    # ------------------------------------------------------------- reductions
    def sum(self, axis=None, keepdims=False):
        if axis is None:
            total = 0.0
            for idx in _iter_indices(self.shape):
                total += self.get(idx)
            shape = (1,) * self.ndim if keepdims else ()
            return NDArray.full(shape, total)
        axes = (axis,) if isinstance(axis, int) else tuple(axis)
        axes = set(a % self.ndim for a in axes)
        if keepdims:
            out_shape = tuple(1 if i in axes else s
                              for i, s in enumerate(self.shape))
        else:
            out_shape = tuple(s for i, s in enumerate(self.shape)
                              if i not in axes)
        res = NDArray.zeros(out_shape)
        for idx in _iter_indices(self.shape):
            if keepdims:
                oidx = tuple(0 if i in axes else idx[i]
                             for i in range(self.ndim))
            else:
                oidx = tuple(idx[i] for i in range(self.ndim)
                             if i not in axes)
            res.set(oidx, res.get(oidx) + self.get(idx))
        return res

    def sum_to_shape(self, shape):
        """Reduce a broadcasted array back to ``shape`` (the adjoint of
        ``broadcast_to``)."""
        shape = tuple(shape)
        g = self
        while g.ndim > len(shape):
            g = g.sum(axis=0)
        for i, (gs, ts) in enumerate(zip(g.shape, shape)):
            if ts == 1 and gs != 1:
                g = g.sum(axis=i, keepdims=True)
            elif ts == 0 and gs != 0:
                raise ShapeError("cannot reduce %s to %s"
                                 % (self.shape, shape))
        if g.shape != shape:
            raise ShapeError("cannot reduce %s to %s" % (self.shape, shape))
        return g

    # -------------------------------------------------------------- elementwise
    def map(self, fn):
        out = NDArray.zeros(self.shape)
        for idx in _iter_indices(self.shape):
            out.set(idx, fn(self.get(idx)))
        return out

    def zip(self, other, fn):
        shape = broadcast_shape(self.shape, other.shape)
        a = self.broadcast_to(shape)
        b = other.broadcast_to(shape)
        out = NDArray.zeros(shape)
        for idx in _iter_indices(shape):
            out.set(idx, fn(a.get(idx), b.get(idx)))
        return out

    # ------------------------------------------------------------------ checks
    def allclose(self, other, rtol=1e-6, atol=1e-8):
        if self.shape != other.shape:
            return False
        for idx in _iter_indices(self.shape):
            a = self.get(idx)
            b = other.get(idx)
            if abs(a - b) > atol + rtol * abs(b):
                return False
        return True

    def __repr__(self):
        return "NDArray(shape=%s, strides=%s, data=%s)" % (
            self.shape, self.strides, self.to_nested())
