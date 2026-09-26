"""Minimal CPU tensor: a typed view over a shared byte storage.

A :class:`Tensor` is ``(storage, dtype, shape, strides, offset)`` where
``offset`` and ``strides`` are in *elements* (strides may be negative).
No autograd, no third-party tensor library -- plain ``struct``/``bytearray``.
"""
from __future__ import annotations

from .dtypes import DType, dtype_of

# Simulated machine-word ceiling: any address arithmetic exceeding this is
# treated as an integer-overflow rejection, even though Python ints are
# arbitrary precision.
MAX_ELEMENTS = (1 << 63) - 1
MAX_NDIM = 32


class Storage:
    """A flat, mutable byte buffer shared by zero or more tensor views."""

    __slots__ = ("_buf",)

    def __init__(self, buf: bytearray):
        if not isinstance(buf, bytearray):
            raise TypeError("Storage expects a bytearray")
        self._buf = buf

    @classmethod
    def allocate(cls, nbytes: int) -> "Storage":
        if not isinstance(nbytes, int) or isinstance(nbytes, bool) or nbytes < 0:
            raise ValueError("nbytes must be a non-negative int")
        return cls(bytearray(nbytes))

    @classmethod
    def from_bytes(cls, data: bytes) -> "Storage":
        return cls(bytearray(data))

    @property
    def nbytes(self) -> int:
        return len(self._buf)

    @property
    def buffer(self) -> bytearray:
        return self._buf


def address_bounds(shape, strides, offset):
    """Return ``(lo, hi)`` reachable element addresses, or ``None`` if empty.

    Works for arbitrary (including negative) strides.  Raises ``ValueError``
    if any intermediate product exceeds the simulated machine-word ceiling.
    """
    lo = hi = offset
    for n, s in zip(shape, strides):
        if n == 0:
            return None
        span = (n - 1) * s
        if abs(span) > MAX_ELEMENTS:
            raise ValueError("address arithmetic overflow")
        if span < 0:
            lo += span
        else:
            hi += span
    return lo, hi


def validate_view(nbytes: int, itemsize: int, shape, strides, offset: int) -> None:
    """Validate that a view is fully reachable inside ``nbytes`` of storage."""
    capacity = nbytes // itemsize
    bounds = address_bounds(shape, strides, offset)
    if bounds is None:  # empty view touches no addresses
        if not 0 <= offset <= capacity:
            raise ValueError("offset outside storage")
        return
    lo, hi = bounds
    if lo < 0:
        raise ValueError("view reaches before start of storage")
    if hi >= capacity:
        raise ValueError("view reaches past end of storage")


def _as_int_tuple(values, what: str):
    out = []
    for v in values:
        if not isinstance(v, int) or isinstance(v, bool):
            raise TypeError(f"{what} must contain ints, got {v!r}")
        out.append(v)
    return tuple(out)


class Tensor:
    __slots__ = ("storage", "dtype", "shape", "strides", "offset")

    def __init__(self, storage: Storage, dtype, shape, strides, offset: int = 0):
        if not isinstance(storage, Storage):
            raise TypeError("storage must be a Storage")
        if isinstance(dtype, str):
            dtype = dtype_of(dtype)
        if not isinstance(dtype, DType):
            raise TypeError("dtype must be a DType or registered name")
        shape = _as_int_tuple(shape, "shape")
        strides = _as_int_tuple(strides, "strides")
        if len(shape) != len(strides):
            raise ValueError("shape and strides must have the same length")
        if len(shape) > MAX_NDIM:
            raise ValueError("too many dimensions")
        for n in shape:
            if n < 0:
                raise ValueError("negative dimension")
            if n > MAX_ELEMENTS:
                raise ValueError("dimension overflow")
        for s in strides:
            if abs(s) > MAX_ELEMENTS:
                raise ValueError("stride overflow")
        if not isinstance(offset, int) or isinstance(offset, bool) or offset < 0:
            raise ValueError("offset must be a non-negative int")
        validate_view(storage.nbytes, dtype.itemsize, shape, strides, offset)
        self.storage = storage
        self.dtype = dtype
        self.shape = shape
        self.strides = strides
        self.offset = offset

    # -- constructors ----------------------------------------------------
    @classmethod
    def zeros(cls, shape, dtype="f8") -> "Tensor":
        dt = dtype_of(dtype) if isinstance(dtype, str) else dtype
        shape = tuple(shape)
        numel = 1
        for n in shape:
            numel *= n
        storage = Storage.allocate(numel * dt.itemsize)
        strides = []
        acc = 1
        for n in reversed(shape):
            strides.append(acc)
            acc *= n
        return cls(storage, dt, shape, tuple(reversed(strides)), 0)

    @classmethod
    def from_flat(cls, values, dtype="f8", shape=None) -> "Tensor":
        dt = dtype_of(dtype) if isinstance(dtype, str) else dtype
        values = list(values)
        if shape is None:
            shape = (len(values),)
        numel = 1
        for n in shape:
            numel *= n
        if numel != len(values):
            raise ValueError("shape does not match value count")
        buf = bytearray()
        for v in values:
            buf += dt.pack(v)
        t = cls.zeros(shape, dt)
        t.storage.buffer[:] = buf
        return t

    # -- views -------------------------------------------------------------
    def as_strided(self, shape, strides, offset=None) -> "Tensor":
        """Create a new validated view sharing this tensor's storage."""
        if offset is None:
            offset = self.offset
        return Tensor(self.storage, self.dtype, shape, strides, offset)

    # -- element access ----------------------------------------------------
    @property
    def ndim(self) -> int:
        return len(self.shape)

    def numel(self) -> int:
        acc = 1
        for n in self.shape:
            acc *= n
        return acc

    def _normalize(self, index):
        if not isinstance(index, tuple):
            index = (index,)
        if len(index) != self.ndim:
            raise IndexError(f"expected {self.ndim} indices, got {len(index)}")
        out = []
        for i, n in zip(index, self.shape):
            if not isinstance(i, int) or isinstance(i, bool):
                raise TypeError("indices must be ints")
            if i < 0:
                i += n
            if not 0 <= i < n:
                raise IndexError("index out of range")
            out.append(i)
        return out

    def _address_of(self, index) -> int:
        addr = self.offset
        for i, s in zip(index, self.strides):
            addr += i * s
        return addr

    def __getitem__(self, index):
        addr = self._address_of(self._normalize(index))
        start = addr * self.dtype.itemsize
        return self.dtype.unpack(bytes(self.storage.buffer[start:start + self.dtype.itemsize]))

    def __setitem__(self, index, value):
        addr = self._address_of(self._normalize(index))
        start = addr * self.dtype.itemsize
        self.storage.buffer[start:start + self.dtype.itemsize] = self.dtype.pack(value)

    # -- iteration ---------------------------------------------------------
    def addresses(self):
        """Yield element addresses in C order (last axis fastest)."""
        if self.numel() == 0:
            return
        ndim = self.ndim
        counters = [0] * ndim
        addr = self.offset
        while True:
            yield addr
            for axis in range(ndim - 1, -1, -1):
                counters[axis] += 1
                addr += self.strides[axis]
                if counters[axis] < self.shape[axis]:
                    break
                counters[axis] = 0
                addr -= self.strides[axis] * self.shape[axis]
            else:
                return

    def values(self):
        itemsize = self.dtype.itemsize
        unpack = self.dtype.unpack
        buf = self.storage.buffer
        for addr in self.addresses():
            start = addr * itemsize
            yield unpack(bytes(buf[start:start + itemsize]))

    def tolist(self):
        def build(axis, it):
            if axis == self.ndim:
                return next(it)
            return [build(axis + 1, it) for _ in range(self.shape[axis])]

        if self.ndim == 0:
            return next(iter(self.values()))
        return build(0, iter(self.values()))

    def __repr__(self):  # pragma: no cover - cosmetic
        return (
            f"Tensor(shape={self.shape}, strides={self.strides}, "
            f"offset={self.offset}, dtype={self.dtype.name!r})"
        )
