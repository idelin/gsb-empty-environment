"""有序、可溢写的行流。

流由一个可迭代对象（列表、生成器、``BoundedSpool.rows()`` 等）构成，
逐行产出 ``(key, payload)`` 二元组，或等价的 ``(key, col1, col2, ...)``
多元组（首元素为键）。流必须按键的非递减序提供，否则读取时报错。
"""

from __future__ import annotations

from collections.abc import Iterable, Iterator
from typing import Any

from .errors import OrderViolationError

# 流耗尽的哨兵。
_END: Any = object()


def key_sort_value(key: Any):
    """SQL 风格的排序值：``None``（SQL NULL）排在最前。

    这样 NULL 键彼此相邻，可被读入同一个键组；但 NULL 永远不参与连接匹配
    （见 ``join.py``）。
    """
    return (0, 0) if key is None else (1, key)


class OrderedStream:
    """惰性有序流，带顺序违约检测。"""

    def __init__(self, source: Iterable[tuple], name: str = "stream"):
        self._it: Iterator[tuple] = iter(source)
        self.name = name
        self._current: Any = _END
        self._prev_key: Any = _END

    def advance(self) -> None:
        """取下一行；耗尽后当前值置为哨兵。"""
        try:
            row = next(self._it)
        except StopIteration:
            self._current = _END
            return
        if not isinstance(row, tuple):
            raise TypeError(f"{self.name}: rows must be tuples, got {type(row).__name__}")
        if len(row) < 1:
            raise ValueError(f"{self.name}: empty row (no key)")
        key = row[0]
        if self._prev_key is not _END:
            # TypeError（不可比较的键）直接向上传播。
            if key_sort_value(key) < key_sort_value(self._prev_key):
                raise OrderViolationError(
                    f"{self.name}: out-of-order key {key!r} after {self._prev_key!r}"
                )
        self._prev_key = key
        self._current = row

    @property
    def exhausted(self) -> bool:
        return self._current is _END

    @property
    def current(self) -> tuple:
        if self._current is _END:
            raise StopIteration(f"{self.name} exhausted")
        return self._current

    @property
    def key(self) -> Any:
        return self.current[0]


def rows_from(source: Iterable[tuple]) -> Iterator[tuple]:
    """便捷包装：直接从可迭代对象读取并校验顺序。"""
    stream = OrderedStream(source)
    while True:
        stream.advance()
        if stream.exhausted:
            return
        yield stream.current
