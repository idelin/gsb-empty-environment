"""Core type system: scalar types, NULL sentinel, schemas and relations.

NULL semantics
--------------
A single ``NULL`` sentinel stands for SQL NULL (unknown). Normal Python
``None`` arriving from JSON is normalized to the same sentinel. Comparisons
against NULL yield NULL rather than a Python boolean (three-valued logic
lives in :mod:`plan_dag.expr`).

Multiset semantics
------------------
A :class:`Relation` is a list of tuple rows, not a set: duplicate rows are
preserved and every operator is row-preserving/bag-oriented. No SQL engine
is involved anywhere in this package.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .errors import TypeMismatchError


class _Null:
    _instance = None

    def __new__(cls):
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:
        return "NULL"

    def __bool__(self) -> bool:
        # Forcing NULL into a Python boolean is a logic error; all callers
        # must implement three-valued logic explicitly.
        raise TypeError("NULL has no boolean truth value; use SQL 3VL rules")

    def __eq__(self, other: object) -> bool:
        return other is self

    def __hash__(self) -> int:
        return hash("__sql_null__")


NULL = _Null()


class DataType:
    INT = "INT"
    FLOAT = "FLOAT"
    TEXT = "TEXT"
    BOOL = "BOOL"

    ALL = frozenset({INT, FLOAT, TEXT, BOOL})


INT = DataType.INT
FLOAT = DataType.FLOAT
TEXT = DataType.TEXT
BOOL = DataType.BOOL


def normalize_value(value):
    """Map JSON ``None`` to the NULL sentinel and leave everything else."""
    if value is None:
        return NULL
    return value


def check_literal(value, declared: str, where: str):
    """Validate that ``value`` is a legal member of scalar type ``declared``."""
    if value is NULL:
        return
    if declared == DataType.INT:
        # bool is a subclass of int in Python; reject it explicitly.
        if not isinstance(value, int) or isinstance(value, bool):
            raise TypeMismatchError(
                f"{where}: value {value!r} is not a valid {DataType.INT}"
            )
    elif declared == DataType.FLOAT:
        if not isinstance(value, (int, float)) or isinstance(value, bool):
            raise TypeMismatchError(
                f"{where}: value {value!r} is not a valid {DataType.FLOAT}"
            )
    elif declared == DataType.TEXT:
        if not isinstance(value, str):
            raise TypeMismatchError(
                f"{where}: value {value!r} is not a valid {DataType.TEXT}"
            )
    elif declared == DataType.BOOL:
        if not isinstance(value, bool):
            raise TypeMismatchError(
                f"{where}: value {value!r} is not a valid {DataType.BOOL}"
            )
    else:
        raise TypeMismatchError(f"{where}: unknown type {declared!r}")


def infer_literal_type(value) -> str:
    """Infer the type of an untyped JSON literal (NULL cannot be inferred)."""
    if isinstance(value, bool):
        return DataType.BOOL
    if isinstance(value, int):
        return DataType.INT
    if isinstance(value, float):
        return DataType.FLOAT
    if isinstance(value, str):
        return DataType.TEXT
    raise TypeMismatchError(
        "untyped NULL literal needs an explicit 'type' field"
    )


@dataclass(frozen=True)
class Column:
    name: str
    data_type: str

    def __post_init__(self):
        if not isinstance(self.name, str) or not self.name:
            raise TypeMismatchError("column name must be a non-empty string")
        if self.data_type not in DataType.ALL:
            raise TypeMismatchError(f"unknown column type {self.data_type!r}")


@dataclass
class Schema:
    columns: tuple[Column, ...]

    def __init__(self, columns):
        cols = tuple(columns)
        for col in cols:
            if not isinstance(col, Column):
                raise TypeMismatchError(f"schema entry is not a Column: {col!r}")
        self.columns = cols

    @classmethod
    def of(cls, *pairs, **named) -> "Schema":
        result: list[Column] = []
        for pair in pairs:
            name, data_type = pair
            result.append(Column(name, data_type))
        for name, data_type in named.items():
            result.append(Column(name, data_type))
        return cls(tuple(result))

    @property
    def names(self) -> tuple[str, ...]:
        return tuple(col.name for col in self.columns)

    @property
    def types(self) -> tuple[str, ...]:
        return tuple(col.data_type for col in self.columns)

    def index_of(self, name: str) -> int:
        matches = [i for i, col in enumerate(self.columns) if col.name == name]
        if not matches:
            return -1
        if len(matches) > 1:
            return -2
        return matches[0]

    def assert_unique_names(self, where: str) -> None:
        seen: set[str] = set()
        for col in self.columns:
            if col.name in seen:
                raise TypeMismatchError(
                    f"{where}: duplicate output column name {col.name!r}"
                )
            seen.add(col.name)

    def with_prefix(self, prefix: str) -> "Schema":
        return Schema(tuple(Column(prefix + col.name, col.data_type) for col in self.columns))

    def __len__(self) -> int:
        return len(self.columns)

    def __eq__(self, other: object) -> bool:
        return isinstance(other, Schema) and self.columns == other.columns


@dataclass
class Relation:
    """An ordered bag of rows over a fixed schema."""

    schema: Schema
    rows: list[tuple] = field(default_factory=list)

    def add_row(self, row) -> None:
        row = tuple(normalize_value(value) for value in row)
        if len(row) != len(self.schema):
            raise TypeMismatchError(
                f"row has {len(row)} values, schema has {len(self.schema)} columns"
            )
        for value, col in zip(row, self.schema.columns):
            check_literal(value, col.data_type, f"row column {col.name!r}")
        self.rows.append(row)

    def __len__(self) -> int:
        return len(self.rows)
