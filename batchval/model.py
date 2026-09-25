"""Data model for the streaming batch validator.

A table is a sequence of batches. Each batch carries typed columns with a
validity bitmap, optional dictionary reference (for dictionary-encoded
strings) and an optional shared-buffer declaration. Everything here is
plain data; validation logic lives in validator.py.
"""
from __future__ import annotations

from dataclasses import dataclass, field, replace
from enum import Enum


class ColType(Enum):
    INT = "int"      # exact arbitrary-precision integers
    FLOAT = "float"  # IEEE doubles; NaN is a value, never a null
    STR = "str"      # dictionary-encoded: column holds integer indices


class Severity(Enum):
    CORRUPT = "corrupt"  # definitely damaged data
    MISSING = "missing"  # data absent; cannot be judged corrupt


class K:
    """Error kinds (stable string identifiers)."""
    SCHEMA = "SCHEMA_MISMATCH"
    LENGTH = "LENGTH_MISMATCH"
    BITMAP = "BITMAP_INVALID"
    BUFFER = "BUFFER_MISSING"
    DICT_REF = "DICT_VERSION_UNKNOWN"
    DICT_STALE = "DICT_VERSION_STALE"
    DICT_INDEX = "DICT_INDEX_OUT_OF_RANGE"
    TYPE = "VALUE_TYPE_MISMATCH"
    NULL = "NULL_INCONSISTENCY"
    SORT = "SORT_VIOLATION"
    SHARED = "SHARED_BUFFER_MISMATCH"


# Row-level kinds are deduplicated stream-wide per column: only the
# earliest bad row is reported (plus an occurrence count).
ROW_LEVEL_KINDS = frozenset({K.NULL, K.TYPE, K.DICT_INDEX})


@dataclass(frozen=True)
class ColumnSpec:
    name: str
    type: ColType
    nullable: bool = True
    sorted_asc: bool = False  # producer declares ascending order


@dataclass(frozen=True)
class Schema:
    columns: tuple[ColumnSpec, ...]

    def names(self) -> tuple[str, ...]:
        return tuple(c.name for c in self.columns)


@dataclass
class Dictionary:
    column: str
    version: int
    values: list[str]


class DictRegistry:
    """Global, versioned dictionary registry shared across batches."""

    def __init__(self) -> None:
        self._dicts: dict[tuple[str, int], Dictionary] = {}

    def register(self, d: Dictionary) -> None:
        self._dicts[(d.column, d.version)] = d

    def get(self, column: str, version: int) -> Dictionary | None:
        return self._dicts.get((column, version))


@dataclass
class ColumnData:
    spec: ColumnSpec
    length: int                 # row count claimed by the column header
    values: list | None         # None = buffer absent (missing, not corrupt)
    bitmap: bytes | None = None  # 1 bit per row, LSB-first; None = all valid
    dict_ref: tuple[str, int] | None = None  # (column, version) for STR


@dataclass
class Batch:
    batch_id: int
    declared_rows: int
    columns: list[ColumnData]
    # buffer label -> names of columns declared to share that buffer
    shared_buffers: dict[str, tuple[str, ...]] = field(default_factory=dict)


@dataclass(frozen=True)
class Error:
    kind: str
    severity: Severity
    batch: int
    row: int | None            # global row number, None for batch-level
    column: str | None
    message: str
    evidence: tuple | None = None   # minimal, bounded slice around the defect
    occurrences: int = 1

    def key(self) -> tuple:
        return (self.kind, self.column, self.row)

    def with_occurrences(self, n: int) -> "Error":
        return replace(self, occurrences=n)


# ---- bitmap helpers -------------------------------------------------------

def pack_bitmap(bits) -> bytes:
    bits = list(bits)
    out = bytearray((len(bits) + 7) // 8)
    for i, b in enumerate(bits):
        if b:
            out[i >> 3] |= 1 << (i & 7)
    return bytes(out)


def bitmap_get(bitmap: bytes, i: int) -> bool:
    return (bitmap[i >> 3] >> (i & 7)) & 1 == 1


def slice_bitmap(bitmap: bytes, start: int, end: int) -> bytes:
    return pack_bitmap(bitmap_get(bitmap, i) for i in range(start, end))
