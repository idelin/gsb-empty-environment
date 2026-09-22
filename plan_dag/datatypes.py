"""Scalar types, fields, and row schemas.

Only JSON-compatible scalars exist: int, float, str, bool, and ``None`` (NULL).
Python ``bool`` is intentionally *not* accepted as INTEGER (a bool is not a
number here); comparisons require matching comparable types.
"""

from __future__ import annotations

from dataclasses import dataclass

from plan_dag.errors import BindingError


class DataType:
    """A named scalar type. Instances compare by identity (singletons)."""

    __slots__ = ("name",)

    def __init__(self, name: str):
        self.name = name

    def __repr__(self) -> str:
        return self.name

    def __eq__(self, other: object) -> bool:
        return self is other

    def __hash__(self) -> int:
        return hash(self.name)


INTEGER = DataType("INTEGER")
DOUBLE = DataType("DOUBLE")
TEXT = DataType("TEXT")
BOOLEAN = DataType("BOOLEAN")
NULL_TYPE = DataType("NULL")

_TYPES = {
    "INTEGER": INTEGER,
    "DOUBLE": DOUBLE,
    "TEXT": TEXT,
    "BOOLEAN": BOOLEAN,
    "NULL": NULL_TYPE,
}


def type_from_name(name: object) -> DataType:
    if not isinstance(name, str) or name not in _TYPES:
        raise BindingError(f"unknown type {name!r}; expected one of {sorted(_TYPES)}")
    return _TYPES[name]


def literal_type(value: object) -> DataType:
    """Infer the type of a JSON literal value.

    ``bool`` is checked before ``int`` on purpose.
    """
    if value is None:
        return NULL_TYPE
    if isinstance(value, bool):
        return BOOLEAN
    if isinstance(value, int):
        return INTEGER
    if isinstance(value, float):
        return DOUBLE
    if isinstance(value, str):
        return TEXT
    raise BindingError(f"unsupported literal value {value!r}")


def is_numeric(t: DataType) -> bool:
    return t in (INTEGER, DOUBLE)


def is_comparable(a: DataType, b: DataType) -> bool:
    """Whether values of the two (non-NULL) types may be compared."""
    if a == b:
        return True
    return is_numeric(a) and is_numeric(b)


def arithmetic_result(t: DataType) -> DataType:
    if not is_numeric(t):
        raise BindingError(f"arithmetic requires INTEGER or DOUBLE, got {t}")
    return t


@dataclass(frozen=True)
class Field:
    """A named, typed column in a row schema."""

    name: str
    type: DataType

    def to_data(self) -> dict:
        return {"name": self.name, "type": self.name}

    @staticmethod
    def from_data(data: object) -> "Field":
        if not isinstance(data, dict):
            raise BindingError(f"field must be an object, got {type(data).__name__}")
        extra = set(data) - {"name", "type"}
        if extra:
            raise BindingError(f"field has unknown keys {sorted(extra)}")
        name = data.get("name")
        if not isinstance(name, str) or not name:
            raise BindingError("field 'name' must be a non-empty string")
        return Field(name=name, type=type_from_name(data.get("type")))


class Schema:
    """An ordered, unique-name list of fields."""

    __slots__ = ("fields", "_index")

    def __init__(self, fields: list[Field]):
        self.fields = list(fields)
        self._index: dict[str, int] = {}
        for i, f in enumerate(self.fields):
            if f.name in self._index:
                raise BindingError(f"duplicate field name {f.name!r} in schema")
            self._index[f.name] = i

    def index_of(self, name: str) -> int:
        if name not in self._index:
            raise BindingError(f"unknown column {name!r}; have {[f.name for f in self.fields]}")
        return self._index[name]

    def type_of(self, name: str) -> DataType:
        return self.fields[self.index_of(name)].type

    def names(self) -> list[str]:
        return [f.name for f in self.fields]

    def to_data(self) -> list[dict]:
        return [f.to_data() for f in self.fields]

    @staticmethod
    def from_data(data: object) -> "Schema":
        if not isinstance(data, list):
            raise BindingError(f"schema must be a list, got {type(data).__name__}")
        return Schema([Field.from_data(f) for f in data])

    def __len__(self) -> int:
        return len(self.fields)

    def __iter__(self):
        return iter(self.fields)

    def __repr__(self) -> str:
        return f"Schema({[(f.name, f.type.name) for f in self.fields]})"
