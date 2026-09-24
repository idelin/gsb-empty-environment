"""Typed column batches: zero-copy nested slicing, copy-on-write, leasing."""
import struct
import threading

from .buffer import Buffer
from .ledger import ClosedError


class ColType:
    __slots__ = ("name", "fmt", "size", "zero")

    def __init__(self, name, fmt, size, zero):
        self.name = name
        self.fmt = fmt
        self.size = size
        self.zero = zero

    def pack(self, value):
        return struct.pack(self.fmt, value)

    def unpack(self, raw):
        return struct.unpack(self.fmt, raw)[0]

    def __repr__(self):
        return f"ColType({self.name})"


INT64 = ColType("int64", "<q", 8, 0)
FLOAT64 = ColType("float64", "<d", 8, 0.0)
BOOL = ColType("bool", "<?", 1, False)


class Schema:
    def __init__(self, columns):
        self.columns = list(columns)
        self.names = [name for name, _ in self.columns]
        if len(set(self.names)) != len(self.names):
            raise ValueError("duplicate column names")

    def index(self, name):
        return self.names.index(name)


class _CompositeBorrow:
    def __init__(self, borrows):
        self._borrows = borrows
        self._closed = False

    def close(self):
        if self._closed:
            return
        self._closed = True
        for borrow in reversed(self._borrows):
            borrow.close()

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()
        return False


class ColumnView:
    """A window over one values buffer and one validity (null bitmap) buffer.

    Contract: a view leases its buffers until release(); releasing the parent
    batch never invalidates live slices (they hold their own refs). null is a
    cleared validity bit, NaN is a stored float value: the two are distinct.
    release()/close() are idempotent; every other operation raises
    ClosedError after release.
    """

    def __init__(self, ctype, values, validity, values_off, validity_off, length):
        self.ctype = ctype
        self._values = values
        self._validity = validity
        self._voff = values_off
        self._valoff = validity_off
        self._length = length
        self._lock = threading.Lock()
        self._released = False

    @classmethod
    def from_data(cls, ledger, ctype, data):
        values = bytearray()
        validity = bytearray()
        for item in data:
            if item is None:
                validity.append(0)
                values += ctype.pack(ctype.zero)
            else:
                validity.append(1)
                values += ctype.pack(item)
        values_buf = Buffer(ledger, values)
        try:
            validity_buf = Buffer(ledger, validity)
        except BaseException:
            values_buf.release()
            raise
        return cls(ctype, values_buf, validity_buf, 0, 0, len(data))

    def _check_open(self):
        if self._released:
            raise ClosedError("column view is released")

    def _check_index(self, i):
        if not 0 <= i < self._length:
            raise IndexError(f"row {i} out of range [0, {self._length})")

    def __len__(self):
        return self._length

    @property
    def is_open(self):
        with self._lock:
            return not self._released

    def buffers(self):
        with self._lock:
            self._check_open()
            return (self._values, self._validity)

    def slice(self, offset, length):
        with self._lock:
            self._check_open()
            if not (0 <= offset and 0 <= length and offset + length <= self._length):
                raise IndexError(
                    f"slice({offset}, {length}) out of range [0, {self._length})"
                )
            self._values.addref()
            try:
                self._validity.addref()
            except BaseException:
                self._values.release()
                raise
            return ColumnView(
                self.ctype,
                self._values,
                self._validity,
                self._voff + offset,
                self._valoff + offset,
                length,
            )

    def get(self, i):
        with self._lock:
            self._check_open()
            self._check_index(i)
            if not self._validity.read(self._valoff + i, 1)[0]:
                return None
            raw = self._values.read(
                (self._voff + i) * self.ctype.size, self.ctype.size
            )
            return self.ctype.unpack(raw)

    def to_list(self):
        with self._lock:
            self._check_open()
            size = self.ctype.size
            raw_values = self._values.read(self._voff * size, self._length * size)
            raw_validity = self._validity.read(self._valoff, self._length)
            out = []
            for i in range(self._length):
                if raw_validity[i]:
                    out.append(self.ctype.unpack(raw_values[i * size:(i + 1) * size]))
                else:
                    out.append(None)
            return out

    def set(self, i, value):
        with self._lock:
            self._check_open()
            self._check_index(i)
            self._ensure_exclusive_locked()
            if value is None:
                self._validity.write(self._valoff + i, b"\x00")
            else:
                self._values.write(
                    (self._voff + i) * self.ctype.size, self.ctype.pack(value)
                )
                self._validity.write(self._valoff + i, b"\x01")

    def _ensure_exclusive_locked(self):
        new_values = None
        new_validity = None
        if self._values.is_shared():
            raw = self._values.read(
                self._voff * self.ctype.size, self._length * self.ctype.size
            )
            new_values = Buffer(self._values.ledger, raw)
        try:
            if self._validity.is_shared():
                raw = self._validity.read(self._valoff, self._length)
                new_validity = Buffer(self._validity.ledger, raw)
        except BaseException:
            if new_values is not None:
                new_values.release()
            raise
        if new_values is not None:
            old = self._values
            self._values = new_values
            self._voff = 0
            old.release()
        if new_validity is not None:
            old = self._validity
            self._validity = new_validity
            self._valoff = 0
            old.release()

    def borrow(self):
        with self._lock:
            self._check_open()
            values_lease = self._values.borrow()
            try:
                validity_lease = self._validity.borrow()
            except BaseException:
                values_lease.close()
                raise
        return _CompositeBorrow([values_lease, validity_lease])

    def release(self):
        with self._lock:
            if self._released:
                return
            self._released = True
            self._values.release()
            self._validity.release()

    close = release


class Batch:
    """A typed table batch: a schema plus one ColumnView per column.

    Contract: slice() shares the parent's physical buffers (counted once in
    the ledger); release()/close() are idempotent and never invalidate live
    slices; construction and slicing are all-or-nothing, partial failures
    release exactly what was acquired.
    """

    def __init__(self, schema, columns):
        self.schema = schema
        self._columns = list(columns)
        self._lock = threading.Lock()
        self._released = False

    @classmethod
    def from_columns(cls, ledger, schema, columns_data):
        if len(columns_data) != len(schema.columns):
            raise ValueError("column data does not match schema")
        lengths = {len(data) for data in columns_data}
        if len(lengths) > 1:
            raise ValueError("columns have different row counts")
        columns = []
        try:
            for (_, ctype), data in zip(schema.columns, columns_data):
                columns.append(ColumnView.from_data(ledger, ctype, data))
        except BaseException:
            for col in columns:
                col.release()
            raise
        return cls(schema, columns)

    def _check_open(self):
        if self._released:
            raise ClosedError("batch is released")

    @property
    def is_open(self):
        with self._lock:
            return not self._released

    @property
    def num_rows(self):
        return len(self._columns[0]) if self._columns else 0

    def columns(self):
        with self._lock:
            return list(self._columns)

    def column(self, name):
        with self._lock:
            self._check_open()
            return self._columns[self.schema.index(name)]

    def slice(self, offset, length):
        with self._lock:
            self._check_open()
            columns = []
            try:
                for col in self._columns:
                    columns.append(col.slice(offset, length))
            except BaseException:
                for col in columns:
                    col.release()
                raise
            return Batch(self.schema, columns)

    def borrow(self):
        with self._lock:
            self._check_open()
            leases = []
            try:
                for col in self._columns:
                    leases.append(col.borrow())
            except BaseException:
                for lease in reversed(leases):
                    lease.close()
                raise
        return _CompositeBorrow(leases)

    def release(self):
        with self._lock:
            if self._released:
                return
            self._released = True
            for col in self._columns:
                col.release()

    close = release
