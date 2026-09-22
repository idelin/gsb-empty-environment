"""有序可溢写流的 merge join（标准库实现，不依赖 SQL 引擎）。"""

from .errors import (
    CancellationToken,
    JoinCancelled,
    MergeJoinError,
    OrderViolationError,
)
from .join import merge_join
from .spool import BoundedSpool
from .stream import OrderedStream, key_sort_value, rows_from

__all__ = [
    "merge_join",
    "BoundedSpool",
    "OrderedStream",
    "key_sort_value",
    "rows_from",
    "CancellationToken",
    "JoinCancelled",
    "MergeJoinError",
    "OrderViolationError",
]
