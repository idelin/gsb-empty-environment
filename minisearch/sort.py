"""多字段排序键：升/降序、null 位置（NULLS FIRST/LAST）、同分时按 ID 稳定。

排序字段用 :class:`SortField` 描述，例如::

    SortField("price", descending=True)                       # 价格降序
    SortField("rating", descending=True, nulls=NULLS_LAST)    # 评分降序，None 最后
    SortField("_score", descending=True)                      # 伪字段：文本相关度

整体排序优先级：按字段声明顺序依次比较；所有字段都相同则按 doc_id 升序，
因此结果是确定且稳定的（同分按 ID）。
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

NULLS_FIRST = "first"
NULLS_LAST = "last"


@dataclass(frozen=True, slots=True)
class SortField:
    """一个排序字段。

    Args:
        name: 文档字段名；``_score`` 表示查询词相关度（由引擎注入）。
        descending: True 为降序，False 为升序（默认）。
        nulls: None 的位置；默认 SQL 约定——升序 LAST、降序 FIRST，
            也可显式指定 NULLS_FIRST / NULLS_LAST。
    """

    name: str
    descending: bool = False
    nulls: str | None = None

    def __post_init__(self) -> None:
        if self.nulls is None:
            object.__setattr__(self, "nulls", NULLS_FIRST if self.descending else NULLS_LAST)
        if self.nulls not in (NULLS_FIRST, NULLS_LAST):
            raise ValueError(f"nulls 必须是 NULLS_FIRST 或 NULLS_LAST，收到 {self.nulls!r}")


class RankKey:
    """可比较的多字段排序键，供 ``list.sort`` 与 ``heapq`` 使用。

    比较语义：对每个字段，先处理 None 位置，再按升/降序比较实际值；
    全部相同则 doc_id 升序者在前。不同类型的值不做隐式强转，
    比较失败会抛出 ``TypeError``（排序字段需保证类型一致）。
    """

    __slots__ = ("doc_id", "values", "fields")

    def __init__(self, doc_id: int, values: dict[str, Any], fields: tuple[SortField, ...]):
        self.doc_id = doc_id
        self.values = values
        self.fields = fields

    def _precedes(self, other: "RankKey") -> bool:
        for spec in self.fields:
            left = self.values.get(spec.name)
            right = other.values.get(spec.name)
            left_null = left is None
            right_null = right is None
            if left_null or right_null:
                if left_null != right_null:
                    return (left_null and spec.nulls == NULLS_FIRST) or (
                        right_null and spec.nulls == NULLS_LAST
                    )
                continue
            if left == right:
                continue
            try:
                less = left < right
            except TypeError:
                raise TypeError(
                    f"字段 {spec.name!r} 的值类型不可比较: {type(left).__name__} 与 "
                    f"{type(right).__name__}"
                ) from None
            return not less if spec.descending else less
        return self.doc_id < other.doc_id

    def __lt__(self, other: "RankKey") -> bool:
        return self._precedes(other)

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, RankKey):
            return NotImplemented
        return self._precedes(other) is False and other._precedes(self) is False
