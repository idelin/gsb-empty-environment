"""CDC schema-versioned change event parsing (standard library only)."""

from .schema import FieldDef, SchemaRegistry, SchemaVersion, UnknownSchemaError
from .events import ABSENT, ParsedChange, RawChangeEvent
from .parser import (
    BoundedBuffer,
    BufferOverflowError,
    ChangeParser,
    FieldTypeError,
    UnknownFieldError,
)

__all__ = [
    "ABSENT",
    "BoundedBuffer",
    "BufferOverflowError",
    "ChangeParser",
    "FieldDef",
    "FieldTypeError",
    "ParsedChange",
    "RawChangeEvent",
    "SchemaRegistry",
    "SchemaVersion",
    "UnknownFieldError",
    "UnknownSchemaError",
]
