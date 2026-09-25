"""Data model: typed column batches, dictionaries, shared buffers, errors."""
from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum


class CType(Enum):
    INT64 = "int64"
    FLOAT64 = "float64"
    UTF8 = "utf8"
    BOOL = "bool"


class Severity(Enum):
    CORRUPT = "corrupt"    # definitely damaged data
    MISSING = "missing"    # referenced data not (yet) available


@dataclass(frozen=True)
class Error:
    kind: str              # schema|length|bitmap|buffer|dict_version|dict_index|type|order
    severity: Severity
    column: str | None
    batch: int             # batch index in the stream
    row: int | None        # global row index across the stream; None for batch-level errors
    message: str
    evidence: tuple = ()   # minimal evidence slice, never a whole column

    def key(self) -> tuple:
        return (self.kind, self.severity.value, self.column, self.batch, self.row)


@dataclass
class BufferRef:
    """Reference into a shared buffer (element offset/length, not bytes)."""
    buffer_id: str
    offset: int
    length: int


@dataclass
class Column:
    name: str
    ctype: CType
    data: list | None = None        # inline values; null slots are marked via validity bitmap
    validity: bytes | None = None   # 1 bit/row, LSB-first; bit 1 = valid, bit 0 = null
    buffer: BufferRef | None = None # shared buffer reference, alternative to inline data
    dict_id: str | None = None      # set => data holds integer dictionary indices
    dict_version: int | None = None


@dataclass
class Batch:
    row_count: int | None           # declared row count; None = not provided (missing data)
    columns: list[Column]


@dataclass
class TableSchema:
    columns: list[tuple[str, CType]]
    sorted: dict[str, str] = field(default_factory=dict)  # name -> "asc" | "desc"
