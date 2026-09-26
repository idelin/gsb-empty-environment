"""整数区间属性索引包：区间语义、UTF-8 文档解析与双端有序索引。"""

from .catalog import (
    CONTAINED_BY,
    CONTAINS,
    INTERSECTS,
    MISSING,
    Catalog,
)
from .document import Document, parse_document, read_document, tokenize
from .interval import INF, Interval, IntervalError, contained_by, contains, intersects

__all__ = [
    "CONTAINED_BY",
    "CONTAINS",
    "INTERSECTS",
    "MISSING",
    "INF",
    "Catalog",
    "Document",
    "Interval",
    "IntervalError",
    "contained_by",
    "contains",
    "intersects",
    "parse_document",
    "read_document",
    "tokenize",
]
