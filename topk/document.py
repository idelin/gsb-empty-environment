"""文档模型：全局唯一 doc_id + 任意字段字典（字段值可为 None）。"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any


@dataclass(frozen=True)
class Document:
    doc_id: int
    fields: dict[str, Any] = field(default_factory=dict)

    def get(self, name: str) -> Any:
        """缺失字段一律视为 None（即 null）。"""
        return self.fields.get(name)

    def __repr__(self) -> str:  # 便于 demo 输出
        return f"Document(doc_id={self.doc_id}, fields={self.fields!r})"
