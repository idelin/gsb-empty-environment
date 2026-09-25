"""Typed record batches with explicit null bitmaps.

A batch is a list of typed columns.  String columns are dictionary
encoded against a :class:`LocalDictionary`; integer columns hold exact
Python ints; float columns hold IEEE doubles where NaN is a *value* and
is never confused with null.  Nullness lives only in the bitmap: the
empty string is an ordinary term, and NaN is an ordinary float.
"""

INT = "int"
FLOAT = "float"
STR = "str"
TYPES = (INT, FLOAT, STR)


def bitmap_new(nrows):
    return bytearray((nrows + 7) // 8)


def bitmap_set(bm, i):
    bm[i >> 3] |= 1 << (i & 7)


def bitmap_get(bm, i):
    return bool(bm[i >> 3] >> (i & 7) & 1)


class Batch:
    def __init__(self, schema, nrows):
        self.schema = [(name, typ) for name, typ in schema]
        for _, typ in self.schema:
            if typ not in TYPES:
                raise ValueError(f"unknown column type {typ!r}")
        self.nrows = nrows
        self.nulls = {name: bitmap_new(nrows) for name, _ in self.schema}
        # int/float columns hold values; str columns hold local codes.
        self.values = {name: [None] * nrows for name, _ in self.schema}
        self.dicts = {}          # str col -> LocalDictionary (runtime only)
        self.dict_versions = {}  # str col -> version at encode time

    # -- cell access ------------------------------------------------------
    def set_null(self, col, row):
        bitmap_set(self.nulls[col], row)

    def is_null(self, col, row):
        return bitmap_get(self.nulls[col], row)

    def set_value(self, col, row, value):
        self.values[col][row] = value

    def get_value(self, col, row):
        return self.values[col][row]

    # -- construction -----------------------------------------------------
    @classmethod
    def from_rows(cls, schema, rows, dicts=None):
        """Build a batch from row tuples; ``None`` becomes a null bit."""
        dicts = dicts or {}
        batch = cls(schema, len(rows))
        for ri, row in enumerate(rows):
            for ci, (name, typ) in enumerate(batch.schema):
                value = row[ci]
                if value is None:
                    batch.set_null(name, ri)
                    continue
                if typ == STR:
                    d = dicts[name]
                    batch.values[name][ri] = d.encode(value)
                    batch.dicts[name] = d
                    batch.dict_versions[name] = d.version
                elif typ == INT:
                    if isinstance(value, bool) or not isinstance(value, int):
                        raise TypeError(f"column {name}: not an int: {value!r}")
                    batch.values[name][ri] = value
                else:  # FLOAT
                    batch.values[name][ri] = float(value)
        return batch


def decode_rows(batch, dicts=None, strict=True):
    """Decode a batch to a list of row tuples (``None`` for nulls)."""
    from .errors import VersionMismatchError

    dicts = dicts if dicts is not None else batch.dicts
    rows = []
    for ri in range(batch.nrows):
        row = []
        for name, typ in batch.schema:
            if batch.is_null(name, ri):
                row.append(None)
                continue
            if typ == STR:
                d = dicts[name]
                recorded = batch.dict_versions.get(name)
                if strict and recorded is not None and recorded != d.version:
                    raise VersionMismatchError(
                        f"column {name}: batch encoded with dictionary "
                        f"version {recorded}, cannot decode with version "
                        f"{d.version}"
                    )
                row.append(d.decode(batch.values[name][ri]))
            else:
                row.append(batch.values[name][ri])
        rows.append(tuple(row))
    return rows
