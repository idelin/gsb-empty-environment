"""本地合成 UTF-8 文本文档集。

文档文件为 UTF-8 编码的 TSV，每行:
    doc_id<TAB>field_name<TAB>text
- doc_id 在全库内唯一；同一 doc_id 的不同行贡献不同字段。
- 空行忽略；字段内 tab 之后的全部内容视为文本（允许文本中出现空白）。
- 字段缺失即该文档没有该字段行（不是空字符串字段）。
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .tokenizer import tokenize


@dataclass(frozen=True)
class Document:
    doc_id: str
    fields: dict[str, str]


def load_corpus(path: str, encoding: str = "utf-8") -> list[Document]:
    merged: dict[str, dict[str, str]] = {}
    order: list[str] = []
    with open(path, "r", encoding=encoding, newline="") as fh:
        for lineno, raw in enumerate(fh, start=1):
            line = raw.rstrip("\n").rstrip("\r")
            if not line.strip():
                continue
            parts = line.split("\t", 2)
            if len(parts) != 3:
                raise ValueError(
                    f"{path}:{lineno}: 每行必须为 doc_id<TAB>field<TAB>text"
                )
            doc_id, field_name, text = parts
            if not doc_id or not field_name:
                raise ValueError(f"{path}:{lineno}: doc_id 与字段名不能为空")
            if doc_id not in merged:
                merged[doc_id] = {}
                order.append(doc_id)
            if field_name in merged[doc_id]:
                raise ValueError(
                    f"{path}:{lineno}: 文档 {doc_id!r} 的字段 {field_name!r} 重复"
                )
            merged[doc_id][field_name] = text
    return [Document(doc_id=d, fields=merged[d]) for d in order]


def field_tokens(doc: Document, field_name: str) -> list[str]:
    text = doc.fields.get(field_name)
    if text is None:
        return []
    return tokenize(text)
