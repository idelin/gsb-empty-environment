"""本地合成 UTF-8 文本文档的解析。

文档格式（UTF-8 纯文本）
------------------------
文件开头是若干行头部元数据，空行之后是正文，例如::

    id: doc-001
    range: [0,5)
    title: 区间索引演示

    正文从这里开始，可以包含中文、English 与数字 12345。

* 头部每行形如 ``键: 值``，键只允许 ASCII 字母、数字、下划线、连字符；
  值取首个冒号之后的全部内容并去除首尾空白。
* ``range`` 是本索引关注的整数区间属性；没有该行、或冒号后为空白，
  表示该属性缺失。``id`` 缺省时由调用方指定（例如文件名）。
* 第一个空行结束头部；其后的全部内容均为正文，头部内不允许空行。

分词规则（正文与通用文本）
--------------------------
不使用任何全文检索库，仅按 Unicode 类别切词，规则固定如下：

1. 文本按 NFKC 做兼容分解，全角字符转为对应半角形式；
2. 一个 token 是以下二者之一：
   - 一个连续的“字母/数字/下划线”序列（Unicode 类别以 L 或 N 开头，
     或字符就是 ``_``），其中中日韩等表意文字按“单字成词”切分；
   - 一个连续的 ASCII 字母数字串（上面的通用规则天然包含该情况）；
3. 空白、标点（含中文标点，如，。！？）一律作为分隔符丢弃；
4. token 统一保留原文大小写，不归一化大小写；纯数字也是 token。

分词结果只用于演示与校验，索引本身不依赖正文分词。
"""

from __future__ import annotations

import os
import unicodedata
from dataclasses import dataclass

from .interval import Interval


KEY_RE = __import__("re").compile(r"[A-Za-z0-9_-]+")


class DocumentError(ValueError):
    """文档格式非法时抛出。"""


@dataclass(frozen=True, slots=True)
class Document:
    doc_id: str
    interval: Interval
    meta: dict[str, str]
    body: str

    def tokens(self) -> list[str]:
        return tokenize(self.body)


def tokenize(text: str) -> list[str]:
    """按模块文档字符串中的固定规则对 UTF-8 文本分词。"""
    normalized = unicodedata.normalize("NFKC", text)
    tokens: list[str] = []
    buf: list[str] = []

    def flush() -> None:
        if buf:
            tokens.append("".join(buf))
            buf.clear()

    for ch in normalized:
        code = ord(ch)
        is_cjk = (
            0x4E00 <= code <= 0x9FFF or      # CJK 统一表意文字
            0x3400 <= code <= 0x4DBF or      # 扩展 A
            0x20000 <= code <= 0x2A6DF or    # 扩展 B
            0xF900 <= code <= 0xFAFF         # 兼容表意文字
        )
        if is_cjk:
            # 中日韩表意文字按单字成词切分。
            flush()
            tokens.append(ch)
        elif ch == "_" or ch.isalnum():
            # 其他 Unicode 字母/数字按下划线连接的连续序列成词。
            buf.append(ch)
        else:
            flush()
    flush()
    return tokens


def parse_document(text: str, *, doc_id: str | None = None,
                   range_key: str = "range") -> Document:
    """解析一篇文档的文本内容。"""
    if "\x00" in text:
        raise DocumentError("文档内容包含 NUL 字节，不是合法文本")
    lines = text.splitlines()
    meta: dict[str, str] = {}
    body_start = 0
    for idx, line in enumerate(lines):
        if line.strip() == "":
            body_start = idx + 1
            break
        if ":" not in line:
            raise DocumentError(f"第 {idx + 1} 行不是 '键: 值' 形式: {line!r}")
        key, value = line.split(":", 1)
        key = key.strip()
        if not KEY_RE.fullmatch(key):
            raise DocumentError(f"第 {idx + 1} 行的键不合法: {key!r}")
        meta[key] = value.strip()
    else:
        # 没有空行分隔符：整个文件都是头部，正文为空。
        body_start = len(lines)

    body = "\n".join(lines[body_start:])
    resolved_id = meta.get("id", doc_id)
    if not resolved_id:
        raise DocumentError("文档缺少 id，且调用方未提供文件名作为 id")
    if resolved_id != resolved_id.strip() or not resolved_id.strip():
        raise DocumentError("文档 id 不能为空")
    interval = Interval.parse(meta.get(range_key, ""))
    return Document(doc_id=resolved_id, interval=interval, meta=meta, body=body)


def read_document(path: str, *, range_key: str = "range",
                  encoding: str = "utf-8") -> Document:
    """从本地文件按 UTF-8 读取并解析一篇文档。"""
    with open(path, "r", encoding=encoding, newline="") as fh:
        text = fh.read()
    fallback_id = os.path.splitext(os.path.basename(path))[0]
    return parse_document(text, doc_id=fallback_id, range_key=range_key)


__all__ = ["Document", "DocumentError", "tokenize",
           "parse_document", "read_document"]
