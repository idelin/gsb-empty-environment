"""索引段：不可变文档集合 + 删除位图，可按排序规格产出有序游标。"""

from __future__ import annotations

from typing import Callable, Iterable, Iterator

from .document import Document


class Segment:
    """一个索引段。

    docs:     段内文档（段一旦构建即不可变，符合 LSM 段语义）
    deleted:  删除位图（doc_id 集合）。命中文档对查询不可见，
              不参与排序与归并，也不会占用 TopK 名额。
    """

    def __init__(self, name: str, docs: Iterable[Document], deleted: Iterable[int] = ()):
        self.name = name
        self._docs = list(docs)
        self.deleted: set[int] = set(deleted)

    def delete(self, doc_id: int) -> None:
        self.deleted.add(doc_id)

    def is_visible(self, doc: Document) -> bool:
        return doc.doc_id not in self.deleted

    def cursor(self, comparator: Callable[[Document, Document], int]) -> Iterator[Document]:
        """按比较器顺序产出段内可见文档的游标。

        段是不可变的，等价于建段时已按该顺序排好序；这里用惰性
        迭代器模拟"逐条读取"，被过滤/删除的项跳过后继续向后读。
        """
        from functools import cmp_to_key

        visible = [d for d in self._docs if self.is_visible(d)]
        visible.sort(key=cmp_to_key(comparator))
        return iter(visible)

    def __len__(self) -> int:
        return len(self._docs)
