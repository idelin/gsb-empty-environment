"""文档模型。"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any


@dataclass(slots=True)
class Document:
    """一条本地文档。

    Attributes:
        doc_id: 全局唯一整数 ID，同分按该 ID 升序稳定排列。
        text: 被分词、建倒排链的 UTF-8 文本内容。
        fields: 排序与过滤用字段；字段值允许为 None。
    """

    doc_id: int
    text: str
    fields: dict[str, Any] = field(default_factory=dict)

    def get(self, name: str, default: Any = None) -> Any:
        return self.fields.get(name, default)
