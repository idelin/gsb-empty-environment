"""Fixed-width scalar dtypes backed by ``struct`` (little-endian).

Only the standard library is used.  Each dtype knows how to pack/unpack a
single scalar to/from exactly ``itemsize`` bytes.
"""
from __future__ import annotations

import struct


class DType:
    __slots__ = ("name", "code", "itemsize", "is_float")

    def __init__(self, name: str, code: str, itemsize: int, is_float: bool):
        self.name = name
        self.code = code
        self.itemsize = itemsize
        self.is_float = is_float

    def pack(self, value) -> bytes:
        return struct.pack("<" + self.code, value)

    def unpack(self, data: bytes):
        return struct.unpack("<" + self.code, data)[0]

    def __repr__(self) -> str:  # pragma: no cover - cosmetic
        return f"DType({self.name!r})"


_REGISTRY = {
    dt.name: dt
    for dt in (
        DType("u1", "B", 1, False),
        DType("i1", "b", 1, False),
        DType("u2", "H", 2, False),
        DType("i2", "h", 2, False),
        DType("u4", "I", 4, False),
        DType("i4", "i", 4, False),
        DType("u8", "Q", 8, False),
        DType("i8", "q", 8, False),
        DType("f4", "f", 4, True),
        DType("f8", "d", 8, True),
    )
}


def dtype_of(name: str) -> DType:
    """Look up a dtype by name, raising ``KeyError`` for unknown names."""
    return _REGISTRY[name]


def is_dtype_name(name) -> bool:
    return isinstance(name, str) and name in _REGISTRY
