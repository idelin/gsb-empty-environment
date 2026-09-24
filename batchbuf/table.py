"""Typed column batches with zero-copy slicing and copy-on-write.

Layout: every column owns one data buffer (fixed-width struct encoding)
and one validity buffer (1 byte per row: 1 = valid, 0 = null).  Null and
NaN are distinct: null is the validity bit being 0, NaN is a valid FLOAT64
payload.  Slices share the parent's physical buffers (extra references),
so the ledger keeps counting each physical buffer once.

Lease contract
--------------
Closing a batch releases only *its own* references.  Slices taken before
the close keep their leases and stay fully usable; the physical buffers
are reclaimed only when the last view referencing them is closed.
``close`` is idempotent: calling it in a loop is safe and releases once.
"""

from __future__ import annotations

import enum
import struct
import threading

from .buffers import Buffer
from .errors import ResourceClosedError
from .ledger import Ledger


class DType(enum.Enum):
    INT64 = "q"
    FLOAT64 = "d"
    BOOL = "?"

    @property
    def size(self) -> int:
        return struct.calcsize("<" + self.value)

    def check(self, value) -> None:
        if self is DType.INT64:
            if isinstance(value, bool) or not isinstance(value, int):
                raise TypeError(f"INT64 column rejects {value!r}")
        elif self is DType.FLOAT64:
            if isinstance(value, bool) or not isinstance(value, (int, float)):
                raise TypeError(f"FLOAT64 column rejects {value!r}")
        elif self is DType.BOOL:
            if not isinstance(value, bool):
                raise TypeError(f"BOOL column rejects {value!r}")


class Schema:
    def __init__(self, fields):
        # fields: iterable of (name, DType)
        self.fields = [(name, DType(dt)) for name, dt in fields]
        names = [n for n, _ in self.fields]
        if len(set(names)) != len(names):
            raise ValueError("duplicate column names in schema")

    def __iter__(self):
        return iter(self.fields)

    def __len__(self):
        return len(self.fields)


class Column:
    """One typed column view; holds exactly one reference per buffer."""

    def __init__(self, name, dtype: DType, buffer: Buffer, offset: int,
                 length: int, validity: Buffer, voffset: int, ledger: Ledger):
        self._name = name
        self._dtype = dtype
        self._buffer = buffer
        self._offset = offset      # element offset into the data buffer
        self._length = length
        self._validity = validity
        self._voffset = voffset    # element offset into the validity buffer
        self._ledger = ledger
        self._lock = threading.Lock()
        self._closed = False

    # -- construction ------------------------------------------------------
    @classmethod
    def from_values(cls, name, dtype: DType, values, ledger: Ledger) -> "Column":
        values = list(values)
        data = bytearray(dtype.size * len(values))
        bits = bytearray(len(values))
        fmt = "<" + dtype.value
        for i, v in enumerate(values):
            if v is None:
                continue  # validity stays 0; payload bytes stay 0
            dtype.check(v)
            bits[i] = 1
            struct.pack_into(fmt, data, i * dtype.size, v)
        # If the second Buffer() raises (quota), the first buffer is still
        # released exactly once via the except path.
        buf = Buffer(data, ledger)
        try:
            vbuf = Buffer(bits, ledger)
        except BaseException:
            buf.release()
            raise
        return cls(name, dtype, buf, 0, len(values), vbuf, 0, ledger)

    # -- state ---------------------------------------------------------------
    def _ensure_open(self) -> None:
        if self._closed:
            raise ResourceClosedError(f"column {self._name!r} is closed")

    @property
    def closed(self) -> bool:
        return self._closed

    @property
    def dtype(self) -> DType:
        return self._dtype

    @property
    def name(self):
        return self._name

    def __len__(self) -> int:
        return self._length

    def buffer_ids(self) -> tuple[int, int]:
        """Physical buffer ids (data, validity) backing this view."""
        return (self._buffer.id, self._validity.id)

    # -- slicing ---------------------------------------------------------------
    def slice(self, offset: int, length: int) -> "Column":
        self._ensure_open()
        if not (0 <= offset and 0 <= length and offset + length <= self._length):
            raise ValueError(f"bad slice ({offset}, {length}) for len {self._length}")
        self._buffer.acquire()
        try:
            self._validity.acquire()
        except BaseException:
            self._buffer.release()
            raise
        return Column(self._name, self._dtype, self._buffer,
                      self._offset + offset, length,
                      self._validity, self._voffset + offset, self._ledger)

    # -- reads ---------------------------------------------------------------
    def is_null(self, i: int) -> bool:
        self._ensure_open()
        self._check_index(i)
        return self._validity.data[self._voffset + i] == 0

    def get(self, i: int):
        self._ensure_open()
        self._check_index(i)
        if self._validity.data[self._voffset + i] == 0:
            return None
        fmt = "<" + self._dtype.value
        return struct.unpack_from(
            fmt, self._buffer.data, (self._offset + i) * self._dtype.size)[0]

    def _check_index(self, i: int) -> None:
        if not 0 <= i < self._length:
            raise IndexError(f"index {i} out of range for length {self._length}")

    def to_list(self) -> list:
        self._ensure_open()
        return [self.get(i) for i in range(self._length)]

    # -- copy-on-write ---------------------------------------------------------
    def set(self, i: int, value) -> None:
        """Write one cell, copying first if the buffer is shared.

        The private copy is charged to the ledger *before* the old
        reference is released, so an over-quota write fails atomically
        with the column untouched.  While both buffers exist the ledger
        (and its peak) accounts for the temporary copy.
        """
        self._ensure_open()
        self._check_index(i)
        if value is not None:
            self._dtype.check(value)
        self._ensure_unique_data()
        self._ensure_unique_validity()
        pos = self._voffset + i
        if value is None:
            self._validity.data[pos] = 0
            return
        self._validity.data[pos] = 1
        fmt = "<" + self._dtype.value
        struct.pack_into(fmt, self._buffer.data,
                         (self._offset + i) * self._dtype.size, value)

    def _ensure_unique_data(self) -> None:
        buf = self._buffer
        if not buf.is_shared():
            return
        n = self._length * self._dtype.size
        start = self._offset * self._dtype.size
        private = bytearray(buf.data[start:start + n])
        new_buf = Buffer(private, self._ledger)  # may raise: nothing changed yet
        buf.release()
        self._buffer = new_buf
        self._offset = 0

    def _ensure_unique_validity(self) -> None:
        buf = self._validity
        if not buf.is_shared():
            return
        private = bytearray(buf.data[self._voffset:self._voffset + self._length])
        new_buf = Buffer(private, self._ledger)  # may raise: nothing changed yet
        buf.release()
        self._validity = new_buf
        self._voffset = 0

    # -- lifecycle -----------------------------------------------------------
    def close(self) -> None:
        """Idempotent: releases each buffer reference exactly once."""
        with self._lock:
            if self._closed:
                return
            self._closed = True
        self._buffer.release()
        self._validity.release()

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()
        return False


class Batch:
    """A row batch: a schema plus one open Column per field."""

    def __init__(self, schema: Schema, columns: list[Column], ledger: Ledger):
        if len(columns) != len(schema):
            raise ValueError("column count does not match schema")
        self._schema = schema
        self._columns = list(columns)
        self._ledger = ledger
        self._by_name = {c.name: c for c in self._columns}
        self._length = len(self._columns[0]) if self._columns else 0
        self._lock = threading.Lock()
        self._closed = False

    @classmethod
    def from_rows(cls, schema: Schema, rows, ledger: Ledger) -> "Batch":
        """Build a batch from row dicts; cleans up exactly once on error."""
        rows = list(rows)
        columns: list[Column] = []
        try:
            for name, dtype in schema:
                columns.append(
                    Column.from_values(name, dtype, (r[name] for r in rows), ledger))
        except BaseException:
            for col in columns:
                col.close()  # each created column released exactly once
            raise
        return cls(schema, columns, ledger)

    # -- state ---------------------------------------------------------------
    def _ensure_open(self) -> None:
        if self._closed:
            raise ResourceClosedError("batch is closed")

    @property
    def closed(self) -> bool:
        return self._closed

    @property
    def schema(self) -> Schema:
        return self._schema

    def __len__(self) -> int:
        return self._length

    def column(self, name) -> Column:
        self._ensure_open()
        return self._by_name[name]

    def buffer_ids(self) -> set[int]:
        self._ensure_open()
        ids: set[int] = set()
        for col in self._columns:
            ids.update(col.buffer_ids())
        return ids

    # -- slicing ---------------------------------------------------------------
    def slice(self, offset: int, length: int) -> "Batch":
        """Zero-copy view; shares physical buffers with this batch."""
        self._ensure_open()
        if not (0 <= offset and 0 <= length and offset + length <= self._length):
            raise ValueError(f"bad slice ({offset}, {length}) for len {self._length}")
        cols: list[Column] = []
        try:
            for col in self._columns:
                cols.append(col.slice(offset, length))
        except BaseException:
            for c in cols:
                c.close()
            raise
        return Batch(self._schema, cols, self._ledger)

    # -- a hand-rolled aggregate (no query engine) -----------------------------
    def sum(self, name):
        """Sum a column, skipping nulls.  INT64 sums are exact Python ints;
        FLOAT64 sums follow IEEE semantics (a NaN payload propagates).
        Returns None when every row is null."""
        col = self.column(name)
        is_int = col.dtype is DType.INT64
        total = 0 if is_int else 0.0
        seen = False
        for i in range(len(col)):
            v = col.get(i)
            if v is None:
                continue
            seen = True
            total += v
        return total if seen else None

    # -- lifecycle -----------------------------------------------------------
    def close(self) -> None:
        """Idempotent.  Slices taken earlier keep their own leases."""
        with self._lock:
            if self._closed:
                return
            self._closed = True
        for col in self._columns:
            col.close()

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()
        return False
