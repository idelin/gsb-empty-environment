"""Pure-Python strided tensor views with alias-safe in-place operations.

A small CPU tensor type built on a flat Python list storage.  Views are
(shape, strides, offset) triples over a shared storage and support
transpose, negative strides, non-contiguous slices and zero-stride
broadcasting.

In-place operations are alias-safe:

* in-place read-modify-write ops (``add_``) raise ``OverlapError`` when
  source and destination may overlap -- indeterminate overwrite is
  forbidden;
* ``copy_`` uses snapshot semantics when the source may overlap the
  destination (source values are materialised before any write);
* ``borrowed()`` locks a storage against writes for the borrow lifetime.

Overlap queries first try fast sufficient conditions (disjoint offset
intervals, a gcd lattice test, a nested-stride injectivity test), then
fall back to an exact finite enumeration of the address set for small
views, and finally answer ``Overlap.UNKNOWN`` which is treated
conservatively as unsafe.

Only the Python standard library is used.
"""

from __future__ import annotations

import itertools
from contextlib import contextmanager
from enum import Enum
from math import gcd, prod

__all__ = [
    "Tensor",
    "Storage",
    "Overlap",
    "TensorError",
    "BoundsError",
    "OverlapError",
    "BorrowError",
    "NonContiguousError",
    "internal_overlap",
    "views_overlap",
    "may_overlap",
    "offset_set",
    "EXACT_ENUM_LIMIT",
]

#: Views with at most this many elements are checked by exact enumeration
#: of their offset (address) set.  Larger ambiguous views get the
#: conservative ``Overlap.UNKNOWN`` answer.
EXACT_ENUM_LIMIT = 4096


class TensorError(Exception):
    """Base class for tensor errors."""


class BoundsError(TensorError, IndexError):
    """A view or index escapes the backing storage."""


class OverlapError(TensorError):
    """In-place operation rejected because of (possible) aliasing."""


class BorrowError(TensorError):
    """Write attempted while the storage is borrowed read-only."""


class NonContiguousError(TensorError):
    """Operation requires a contiguous layout."""


class Overlap(Enum):
    """Tri-state overlap answer; UNKNOWN is conservatively unsafe."""

    NO = "no"
    YES = "yes"
    UNKNOWN = "unknown"

    def __bool__(self):  # anything but NO is "may overlap"
        return self is not Overlap.NO


class Storage:
    """A flat element buffer plus a borrow counter for write protection."""

    __slots__ = ("data", "borrows")

    def __init__(self, data):
        self.data = list(data)
        self.borrows = 0

    def __len__(self):
        return len(self.data)


# ---------------------------------------------------------------------------
# offset helpers
# ---------------------------------------------------------------------------

def _offset_bounds(shape, strides):
    """(min, max) relative offset reachable by the view (empty -> (0, 0))."""
    lo = hi = 0
    for n, s in zip(shape, strides):
        if n == 0:
            return 0, 0
        ext = (n - 1) * s
        if s >= 0:
            hi += ext
        else:
            lo += ext
    return lo, hi


def _iter_offsets(shape, strides, offset=0):
    """Yield the storage offset of every element in row-major index order."""
    if 0 in shape:
        return
    for idx in itertools.product(*(range(n) for n in shape)):
        yield offset + sum(i * s for i, s in zip(idx, strides))


def offset_set(t):
    """Exact set of storage offsets addressed by view ``t`` (small views)."""
    return set(_iter_offsets(t.shape, t.strides, t.offset))


def _contiguous_strides(shape):
    strides = []
    acc = 1
    for n in reversed(shape):
        strides.append(acc)
        acc *= n
    return tuple(reversed(strides))


# ---------------------------------------------------------------------------
# overlap analysis
# ---------------------------------------------------------------------------

def internal_overlap(t, exact_limit=EXACT_ENUM_LIMIT):
    """Can two distinct indices of ``t`` address the same storage cell?

    Zero-stride broadcast dims (size > 1, stride 0) alias by construction.
    Otherwise a nested-stride sufficient condition is tried, then exact
    finite enumeration, then a conservative UNKNOWN.
    """
    if t.numel <= 1:  # empty views and single elements cannot self-alias
        return Overlap.NO
    dims = [(n, s) for n, s in zip(t.shape, t.strides) if n > 1]
    if not dims:
        return Overlap.NO
    if any(s == 0 for _, s in dims):
        return Overlap.YES
    if len(dims) == 1:
        return Overlap.NO
    # Sufficient injectivity condition: sorted |stride| m_k must exceed the
    # total offset span covered by all smaller strides.
    covered = 0
    for m, n in sorted((abs(s), n) for n, s in dims):
        if m <= covered:
            break
        covered += (n - 1) * m
    else:
        return Overlap.NO
    if t.numel <= exact_limit:
        seen = set()
        for off in _iter_offsets(t.shape, t.strides, 0):
            if off in seen:
                return Overlap.YES
            seen.add(off)
        return Overlap.NO
    return Overlap.UNKNOWN


def views_overlap(a, b, exact_limit=EXACT_ENUM_LIMIT):
    """Do views ``a`` and ``b`` address at least one common storage cell?"""
    if a.storage is not b.storage:
        return Overlap.NO
    if a.numel == 0 or b.numel == 0:
        return Overlap.NO
    # Fast sufficient condition 1: disjoint bounding intervals.
    a_lo, a_hi = a.absolute_bounds()
    b_lo, b_hi = b.absolute_bounds()
    if a_hi < b_lo or b_hi < a_lo:
        return Overlap.NO
    # Fast sufficient condition 2: gcd lattice -- if every stride is a
    # multiple of g but the base offsets differ mod g, the views live on
    # disjoint address lattices (e.g. even/odd strided slices).
    g = 0
    for s in a.strides + b.strides:
        g = gcd(g, abs(s))
    if g > 1 and (a.offset - b.offset) % g != 0:
        return Overlap.NO
    # Exact finite solver: enumerate both address sets and intersect.
    if a.numel + b.numel <= exact_limit:
        return Overlap.YES if offset_set(a) & offset_set(b) else Overlap.NO
    return Overlap.UNKNOWN


def may_overlap(a, b):
    """Conservative boolean: True unless overlap is proven impossible."""
    return views_overlap(a, b) is not Overlap.NO


# ---------------------------------------------------------------------------
# tensor
# ---------------------------------------------------------------------------

class Tensor:
    """A strided view over a shared :class:`Storage`."""

    __slots__ = ("storage", "shape", "strides", "offset")

    def __init__(self, storage, shape, strides=None, offset=0):
        shape = tuple(int(d) for d in shape)
        if any(d < 0 for d in shape):
            raise ValueError(f"negative dimension in shape {shape}")
        if strides is None:
            strides = _contiguous_strides(shape)
        strides = tuple(int(s) for s in strides)
        if len(strides) != len(shape):
            raise ValueError("shape/strides rank mismatch")
        self.storage = storage
        self.shape = shape
        self.strides = strides
        self.offset = int(offset)
        self._validate_bounds()

    # -- constructors ------------------------------------------------------

    @classmethod
    def from_flat(cls, data, shape):
        if prod(shape) != len(data):
            raise ValueError("data length does not match shape")
        return cls(Storage(data), shape)

    @classmethod
    def zeros(cls, shape):
        return cls(Storage([0] * prod(shape)), shape)

    @classmethod
    def arange(cls, n):
        return cls(Storage(list(range(n))), (n,))

    # -- basic properties --------------------------------------------------

    @property
    def ndim(self):
        return len(self.shape)

    @property
    def numel(self):
        return prod(self.shape)

    @property
    def is_contiguous(self):
        if self.numel <= 1:
            return True
        expected = 1
        for n, s in zip(reversed(self.shape), reversed(self.strides)):
            if n == 1:
                continue
            if s != expected:
                return False
            expected *= n
        return True

    def absolute_bounds(self):
        """(min, max) absolute storage offset addressed by this view."""
        lo, hi = _offset_bounds(self.shape, self.strides)
        return self.offset + lo, self.offset + hi

    def _validate_bounds(self):
        # Bounds are checked against the real address extent of the view,
        # never against the element count alone.
        if self.numel == 0:
            return
        lo, hi = self.absolute_bounds()
        if lo < 0 or hi >= len(self.storage.data):
            raise BoundsError(
                f"view addresses offsets [{lo}, {hi}] but storage holds "
                f"{len(self.storage.data)} elements"
            )

    # -- element access ----------------------------------------------------

    def _checked_offset(self, index):
        if isinstance(index, int):
            index = (index,)
        if len(index) != self.ndim:
            raise BoundsError(f"expected {self.ndim} indices, got {len(index)}")
        off = self.offset
        for ax, (i, n, s) in enumerate(zip(index, self.shape, self.strides)):
            if not 0 <= i < n:
                raise BoundsError(
                    f"index {i} out of range for dim {ax} of size {n}"
                )
            off += i * s
        if not 0 <= off < len(self.storage.data):
            raise BoundsError(f"offset {off} outside storage")
        return off

    def get(self, *index):
        return self.storage.data[self._checked_offset(index)]

    def set(self, index, value):
        self._check_writable()
        self.storage.data[self._checked_offset(index)] = value

    # -- views ---------------------------------------------------------------

    def __getitem__(self, key):
        if not isinstance(key, tuple):
            key = (key,)
        key = list(key)
        if key.count(Ellipsis) > 1:
            raise IndexError("only one ellipsis allowed")
        n_given = sum(1 for k in key if k is not Ellipsis)
        if n_given > self.ndim:
            raise IndexError("too many indices")
        if Ellipsis in key:
            i = key.index(Ellipsis)
            key[i : i + 1] = [slice(None)] * (self.ndim - n_given)
        key += [slice(None)] * (self.ndim - len(key))

        new_shape, new_strides = [], []
        offset = self.offset
        for ax, k in enumerate(key):
            n, s = self.shape[ax], self.strides[ax]
            if isinstance(k, int):
                if k < 0:
                    k += n
                if not 0 <= k < n:
                    raise BoundsError(f"index {k} out of range for dim {ax}")
                offset += k * s
            elif isinstance(k, slice):
                start, stop, step = k.indices(n)
                length = len(range(start, stop, step))
                if length:
                    offset += start * s
                new_shape.append(length)
                new_strides.append(s * step)
            else:
                raise TypeError(f"unsupported index {k!r}")
        return Tensor(self.storage, new_shape, new_strides, offset)

    def transpose(self, dim0=0, dim1=1):
        dim0 %= self.ndim
        dim1 %= self.ndim
        shape = list(self.shape)
        strides = list(self.strides)
        shape[dim0], shape[dim1] = shape[dim1], shape[dim0]
        strides[dim0], strides[dim1] = strides[dim1], strides[dim0]
        return Tensor(self.storage, shape, strides, self.offset)

    def permute(self, *dims):
        if len(dims) == 1 and isinstance(dims[0], (tuple, list)):
            dims = tuple(dims[0])
        dims = tuple(d % self.ndim for d in dims)
        if sorted(dims) != list(range(self.ndim)):
            raise ValueError(f"{dims} is not a permutation")
        return Tensor(
            self.storage,
            tuple(self.shape[d] for d in dims),
            tuple(self.strides[d] for d in dims),
            self.offset,
        )

    @property
    def T(self):
        return self.permute(*reversed(range(self.ndim)))

    def broadcast_to(self, shape):
        shape = tuple(int(d) for d in shape)
        if len(shape) < self.ndim:
            raise ValueError("cannot broadcast to fewer dims")
        pad = len(shape) - self.ndim
        strides = [0] * pad
        for i, n in enumerate(self.shape):
            t = shape[pad + i]
            if t == n:
                strides.append(self.strides[i])
            elif n == 1:
                strides.append(0)  # zero-stride broadcast
            else:
                raise ValueError(f"cannot broadcast {self.shape} to {shape}")
        return Tensor(self.storage, shape, strides, self.offset)

    def reshape(self, *shape):
        if len(shape) == 1 and isinstance(shape[0], (tuple, list)):
            shape = tuple(shape[0])
        shape = tuple(int(d) for d in shape)
        if prod(shape) != self.numel:
            raise ValueError(f"cannot reshape {self.shape} to {shape}")
        if not self.is_contiguous:
            raise NonContiguousError(
                "reshape of a non-contiguous view requires a copy; "
                "use .contiguous().reshape(...) explicitly"
            )
        return Tensor(self.storage, shape, None, self.offset)

    def contiguous(self):
        flat = [
            self.storage.data[off]
            for off in _iter_offsets(self.shape, self.strides, self.offset)
        ]
        return Tensor(Storage(flat), self.shape)

    # -- conversion ----------------------------------------------------------

    def to_list(self):
        def build(dim, off):
            if dim == self.ndim:
                return self.storage.data[off]
            return [
                build(dim + 1, off + i * self.strides[dim])
                for i in range(self.shape[dim])
            ]

        return build(0, self.offset)

    def __iter__(self):
        for i in range(self.shape[0] if self.shape else 0):
            yield self[i]

    def __len__(self):
        return self.shape[0] if self.shape else 0

    def __repr__(self):
        return (
            f"Tensor(shape={self.shape}, strides={self.strides}, "
            f"offset={self.offset}, data={self.to_list()!r})"
        )

    # -- overlap queries -----------------------------------------------------

    def internal_overlap(self, exact_limit=EXACT_ENUM_LIMIT):
        return internal_overlap(self, exact_limit)

    def overlaps(self, other, exact_limit=EXACT_ENUM_LIMIT):
        return views_overlap(self, other, exact_limit)

    # -- borrow-based write protection ----------------------------------------

    def _check_writable(self):
        if self.storage.borrows:
            raise BorrowError(
                "storage is borrowed read-only; writes are forbidden "
                "until the borrow ends"
            )

    @contextmanager
    def borrowed(self):
        """Read-only borrow: writes through ANY view of this storage fail."""
        self.storage.borrows += 1
        try:
            yield self
        finally:
            self.storage.borrows -= 1

    # -- in-place ops -----------------------------------------------------------

    def fill_(self, value):
        self._check_writable()
        for off in _iter_offsets(self.shape, self.strides, self.offset):
            self.storage.data[off] = value
        return self

    def copy_(self, src):
        """Copy ``src`` into ``self`` with snapshot semantics on overlap."""
        self._check_writable()
        if src.shape != self.shape:
            try:
                src = src.broadcast_to(self.shape)
            except ValueError:
                raise ValueError(
                    f"shape mismatch: {src.shape} -> {self.shape}"
                ) from None
        if internal_overlap(self) is not Overlap.NO:
            raise OverlapError(
                "destination view aliases itself; in-place copy would "
                "be ambiguous"
            )
        dst_offs = list(_iter_offsets(self.shape, self.strides, self.offset))
        src_offs = list(_iter_offsets(src.shape, src.strides, src.offset))
        if views_overlap(self, src) is not Overlap.NO:
            # Snapshot semantics: materialise every source value BEFORE any
            # destination cell is written, so the result is independent of
            # evaluation order.
            values = [src.storage.data[o] for o in src_offs]
            for off, v in zip(dst_offs, values):
                self.storage.data[off] = v
        else:
            for do, so in zip(dst_offs, src_offs):
                self.storage.data[do] = src.storage.data[so]
        return self

    def add_(self, other):
        """In-place add; forbidden when aliasing makes the result
        depend on evaluation order."""
        self._check_writable()
        if other.shape != self.shape:
            try:
                other = other.broadcast_to(self.shape)
            except ValueError:
                raise ValueError(
                    f"shape mismatch: {other.shape} -> {self.shape}"
                ) from None
        for t in (self, other):
            if internal_overlap(t) is not Overlap.NO:
                raise OverlapError(
                    "operand aliases itself; in-place add is indeterminate"
                )
        ov = views_overlap(self, other)
        if ov is not Overlap.NO:
            raise OverlapError(
                f"in-place add with {ov.value} overlap is forbidden"
            )
        for do, so in zip(
            _iter_offsets(self.shape, self.strides, self.offset),
            _iter_offsets(other.shape, other.strides, other.offset),
        ):
            self.storage.data[do] += other.storage.data[so]
        return self
