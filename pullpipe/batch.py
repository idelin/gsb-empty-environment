"""有界批次。

关键约定：
* ``Batch`` 是「本批数据」，可以为空——空批次是合法的心跳，不等于 EOF。
* ``EOF`` 是 None：``next_batch`` 返回 None 才表示流结束。
* 因此特意让空 Batch 的布尔值仍为 True，防止 ``if not batch`` 误把空批当 EOF。
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Iterable

Row = tuple
EOF: None = None


@dataclass(slots=True)
class Batch:
    rows: list[Row]

    def __init__(self, rows: Iterable[Row] = ()):
        self.rows = list(rows)

    def __len__(self) -> int:
        return len(self.rows)

    def __iter__(self):
        return iter(self.rows)

    def __bool__(self) -> bool:
        # 空批次依然“存在”：只有 None 才是 EOF。
        return True
