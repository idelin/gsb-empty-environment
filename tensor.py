"""Small CPU tensor library (storage + strided views), standard library only."""

from __future__ import annotations

import struct
from dataclasses import dataclass
from typing import Iterable, Sequence


@dataclass(frozen=True)
class DType:
    name: str
    code: int
    itemsize: int
    kind: str  # 'i' signed int, 'u' unsigned int, 'f' float
    struct_fmt: str

    def __repr__(self) -> str:
        return f"dtype({self.name})"


_DTYPES_BY_NAME: dict[str, DType] = {}
_DTYPES_BY_CODE: dict[int, DType] = {}


def _reg(name: str, code: int, itemsize: int, kind: str, fmt: str) -> DType:
    dt = DType(name, code, itemsize, kind, fmt)
    _DTYPES_BY_NAME[name] = dt
    _DTYPES_BY_CODE[code] = dt
    return dt


int8 = _reg("int8", 1, 1, "i", "b")
uint8 = _reg("uint8", 2, 1, "u", "B")
int16 = _reg("int16", 3, 2, "i", "h")
uint16 = _reg("uint16", 4, 2, "u", "H")
int32 = _reg("int32", 5, 4, "i", "i")
uint32 = _reg("uint32", 6, 4, "u", "I")
int64 = _reg("int64", 7, 8, "i", "q")
uint64 = _reg("uint64", 8, 8, "u", "Q")
float32 = _reg("float32", 9, 4, "f", "f")
float64 = _reg("float64", 10, 8, "f", "d")


def dtype_by_name(name: str) -> DType:
    try:
        return _DTYPES_BY_NAME[name]
    except KeyError:
        raise ValueError(f"unknown dtype {name!r}")


def dtype_by_code(code: int) -> DType:
    try:
        return _DTYPES_BY_CODE[code]
    except KeyError:
        raise ValueError(f"unknown dtype code {code}")


class LayoutError(ValueError):
    """Raised for invalid tensor layouts (bad shape/stride/offset, OOB)."""


def _checked_mul_product(shape: Sequence[int], element_budget: int) -> int:
    """Product of shape dims; rejects negatives and budgets (guards overflow bombs)."""
    n = 1
    for d in shape:
        if not isinstance(d, int) or isinstance(d, bool):
            raise LayoutError("shape dimensions must be ints")
        if d < 0:
            raise LayoutError(f"negative shape dimension {d}")
        if d == 0:
            return 0
        if n > element_budget // d:
            raise LayoutError("shape product exceeds element budget")
        n *= d
    return n


def reachable_byte_span(
    shape: Sequence[int], strides: Sequence[int], dtype: DType
) -> tuple[int, int, int]:
    """Return (lo, hi_exclusive, first_elem_offset_bytes) for the addressed region."""
    if len(shape) != len(strides):
        raise LayoutError("shape and strides must have equal length")
    lo = 0
    hi = 0
    for d, s in zip(shape, strides):
        if not isinstance(s, int) or isinstance(s, bool):
            raise LayoutError("strides must be ints")
        if d <= 1:
            continue
        if s >= 0:
            hi += (d - 1) * s
        else:
            lo += (d - 1) * s  # negative
    if hi < 0:
        hi = 0
    if lo > 0:
        lo = 0
    return lo, hi + dtype.itemsize, -lo


def validate_layout(
    shape: Sequence[int],
    strides: Sequence[int],
    dtype: DType,
    offset: int,
    storage_nbytes: int,
    element_budget: int | None = None,
) -> int:
    """Validate a strided layout against its backing storage. Returns element count."""
    if not isinstance(dtype, DType):
        raise LayoutError("dtype must be a DType")
    if not isinstance(offset, int) or isinstance(offset, bool):
        raise LayoutError("offset must be an int")
    if offset < 0:
        raise LayoutError(f"negative storage offset {offset}")
    if offset % dtype.itemsize:
        raise LayoutError("offset is not aligned to dtype itemsize")
    if len(shape) > 32:
        raise LayoutError("too many dimensions (max 32)")
    n = _checked_mul_product(shape, element_budget if element_budget is not None else (2**63 - 1))
    if shape:
        lo, hi, _ = reachable_byte_span(shape, strides, dtype)
        start = offset + lo
        end = offset + hi
        for s in strides:
            if s % dtype.itemsize:
                raise LayoutError("stride not aligned to dtype itemsize")
        if start < 0:
            raise LayoutError("negative-stride region starts before storage")
        if end > storage_nbytes:
            raise LayoutError(
                f"view addresses [0x{start:x}, 0x{end:x}) exceed storage of {storage_nbytes} bytes"
            )
    elif offset + dtype.itemsize > storage_nbytes:
        raise LayoutError("scalar offset is outside storage")
    return n


class Storage:
    """Raw byte buffer shared by tensor views."""

    __slots__ = ("data",)

    def __init__(self, data: bytearray | bytes):
        self.data = bytearray(data)

    @property
    def nbytes(self) -> int:
        return len(self.data)

    def __len__(self) -> int:
        return len(self.data)


class Tensor:
    """Strided CPU tensor view over a Storage."""

    __slots__ = ("storage", "dtype", "shape", "strides", "offset")

    def __init__(
        self,
        storage: Storage,
        dtype: DType | str,
        shape: Sequence[int],
        strides: Sequence[int] | None = None,
        offset: int = 0,
        *,
        element_budget: int | None = None,
    ):
        if not isinstance(storage, Storage):
            raise LayoutError("storage must be a Storage")
        if isinstance(dtype, str):
            dtype = dtype_by_name(dtype)
        shape = tuple(int(d) for d in shape)
        if strides is None:
            strides = _c_strides(shape, dtype.itemsize)
        else:
            strides = tuple(int(s) for s in strides)
        validate_layout(shape, strides, dtype, int(offset), storage.nbytes, element_budget)
        self.storage = storage
        self.dtype = dtype
        self.shape = shape
        self.strides = strides
        self.offset = int(offset)

    @property
    def ndim(self) -> int:
        return len(self.shape)

    @property
    def size(self) -> int:
        n = 1
        for d in self.shape:
            n *= d
        return n

    @property
    def nbytes(self) -> int:
        return self.size * self.dtype.itemsize

    def _address(self, idx: Sequence[int]) -> int:
        return self.offset + sum(i * s for i, s in zip(idx, self.strides))

    def __getitem__(self, key) -> "Tensor":
        if not isinstance(key, tuple):
            key = (key,)
        if len(key) > self.ndim:
            raise IndexError("too many indices")
        new_shape = list(self.shape)
        new_strides = list(self.strides)
        new_offset = self.offset
        out_shape: list[int] = []
        out_strides: list[int] = []
        dim = 0
        for k in key:
            length = self.shape[dim]
            stride = self.strides[dim]
            if isinstance(k, int):
                if k < 0:
                    k += length
                if not 0 <= k < length:
                    raise IndexError("index out of range")
                new_offset += k * stride
                dim += 1
            elif isinstance(k, slice):
                start, stop, step = k.indices(length)
                if step == 0:
                    raise IndexError("slice step cannot be zero")
                if step > 0:
                    count = max(0, (stop - start + step - 1) // step)
                else:
                    count = max(0, (start - stop - step - 1) // (-step))
                if count:
                    new_offset += start * stride
                out_shape.append(count)
                out_strides.append(step * stride)
                dim += 1
            else:
                raise IndexError("unsupported index type")
        while dim < self.ndim:
            out_shape.append(new_shape[dim])
            out_strides.append(new_strides[dim])
            dim += 1
        return Tensor(self.storage, self.dtype, tuple(out_shape), tuple(out_strides), new_offset)

    def item(self, idx: Sequence[int]):
        idx = tuple(idx) if not isinstance(idx, tuple) else idx
        if len(idx) != self.ndim:
            raise IndexError("expected ndim indices")
        addr = self._address(idx)
        return struct.unpack_from(self.dtype.struct_fmt, self.storage.data, addr)[0]

    def flat_values(self) -> list:
        """Values in logical element order (C order of this view's shape)."""
        return [self.item(i) for i in _iter_indices(self.shape)]

    def tolist(self):
        vals = self.flat_values()
        return _nest(vals, self.shape)

    def is_c_contiguous(self) -> bool:
        return tuple(self.strides) == _c_strides(self.shape, self.dtype.itemsize)

    def __repr__(self) -> str:
        return f"Tensor(shape={self.shape}, dtype={self.dtype.name}, strides={self.strides}, offset={self.offset})"


def _c_strides(shape: Sequence[int], itemsize: int) -> tuple[int, ...]:
    strides: list[int] = []
    s = itemsize
    for d in reversed(shape):
        strides.append(s if d != 0 else s)
        s *= d if d != 0 else 1
    return tuple(reversed(strides))


def _iter_indices(shape: Sequence[int]):
    if not shape:
        yield ()
        return
    idx = [0] * len(shape)
    total = 1
    for d in shape:
        total *= d
    for _ in range(total):
        yield tuple(idx)
        for axis in range(len(shape) - 1, -1, -1):
            idx[axis] += 1
            if idx[axis] < shape[axis]:
                break
            idx[axis] = 0


def _nest(vals: list, shape: Sequence[int]):
    if not shape:
        return vals[0]
    if len(shape) == 1:
        return list(vals)
    block = 1
    for d in shape[1:]:
        block *= d
    return [_nest(vals[i * block:(i + 1) * block], shape[1:]) for i in range(shape[0])]


def from_values(values: Iterable, dtype: DType | str, shape: Sequence[int] | None = None) -> Tensor:
    if isinstance(dtype, str):
        dtype = dtype_by_name(dtype)
    flat = [int(v) if dtype.kind in "iu" else float(v) for v in values] if _is_scalar_iter(
        values
    ) else _flatten(values)
    if shape is None:
        shape = (len(flat),)
    n = 1
    for d in shape:
        n *= d
    if n != len(flat):
        raise LayoutError(f"value count {len(flat)} does not match shape product {n}")
    buf = bytearray(n * dtype.itemsize)
    for i, v in enumerate(flat):
        struct.pack_into(dtype.struct_fmt, buf, i * dtype.itemsize, v)
    return Tensor(Storage(buf), dtype, tuple(shape))


def _is_scalar_iter(values) -> bool:
    return all(not isinstance(v, (list, tuple)) for v in values)


def _flatten(values) -> list:
    out: list = []
    for v in values:
        if isinstance(v, (list, tuple)):
            out.extend(_flatten(v))
        else:
            out.append(v)
    return out


def arange(n: int, dtype: DType | str = int32) -> Tensor:
    if isinstance(dtype, str):
        dtype = dtype_by_name(dtype)
    return from_values(range(n), dtype, (n,))


def reshape_contiguous(t: Tensor, shape: Sequence[int]) -> Tensor:
    """Reshape only when the view is C-contiguous (otherwise raises)."""
    if not t.is_c_contiguous():
        raise LayoutError("reshape requires a C-contiguous view")
    shape = list(shape)
    neg = [i for i, d in enumerate(shape) if d == -1]
    if len(neg) > 1:
        raise LayoutError("only one dimension may be -1")
    known = 1
    for d in shape:
        if d != -1:
            known *= d
    if neg:
        if known == 0 or t.size % known:
            raise LayoutError("cannot infer -1 dimension")
        shape[neg[0]] = t.size // known
    prod = 1
    for d in shape:
        prod *= d
    if prod != t.size:
        raise LayoutError("reshape product mismatch")
    return Tensor(t.storage, t.dtype, tuple(shape), offset=t.offset)
