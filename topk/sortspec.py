"""多字段排序规格：每字段独立升降序与 null 位置，同分按文档 ID 稳定。"""

from __future__ import annotations

from dataclasses import dataclass
from functools import cmp_to_key
from typing import Any, Callable


@dataclass(frozen=True)
class SortField:
    """一个排序字段。

    name:        文档字段名
    desc:        True 为降序，False 为升序
    nulls_first: True 表示 NULLS FIRST，False 表示 NULLS LAST。
                 null 位置与升降序相互独立（同 SQL 的 NULLS FIRST/LAST）。
    """

    name: str
    desc: bool = False
    nulls_first: bool = False


def _compare_values(a: Any, b: Any, desc: bool, nulls_first: bool) -> int:
    if a is None and b is None:
        return 0
    if a is None:
        return -1 if nulls_first else 1
    if b is None:
        return 1 if nulls_first else -1
    if a < b:
        return 1 if desc else -1
    if a > b:
        return -1 if desc else 1
    return 0


def make_comparator(sort_fields: list[SortField]) -> Callable[[Any, Any], int]:
    """生成文档比较函数。

    依次比较各排序字段；全部相同（同分）时按 doc_id 升序兜底，
    保证全局顺序确定、跨段稳定。
    """

    def compare(doc_a: Any, doc_b: Any) -> int:
        for field in sort_fields:
            result = _compare_values(
                doc_a.get(field.name),
                doc_b.get(field.name),
                field.desc,
                field.nulls_first,
            )
            if result != 0:
                return result
        # 同分稳定：按文档 ID 升序
        return (doc_a.doc_id > doc_b.doc_id) - (doc_a.doc_id < doc_b.doc_id)

    return compare


def sort_key(sort_fields: list[SortField]):
    """返回可直接传给 sorted() 的 key（基于 cmp_to_key）。"""
    return cmp_to_key(make_comparator(sort_fields))
