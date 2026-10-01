"""正则 AST 节点定义。每个节点都保留源码位置 span（半开区间 [start, end)）。"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Span:
    start: int
    end: int


@dataclass(frozen=True)
class EmptyLang:
    """空语言：不匹配任何字符串（与空串严格区分）。"""
    span: Span


@dataclass(frozen=True)
class Epsilon:
    """空串：只匹配 ""。"""
    span: Span


@dataclass(frozen=True)
class Literal:
    span: Span
    char: str


@dataclass(frozen=True)
class CharClass:
    span: Span
    chars: frozenset
    negated: bool


@dataclass(frozen=True)
class Concat:
    span: Span
    parts: tuple


@dataclass(frozen=True)
class Alt:
    span: Span
    branches: tuple


@dataclass(frozen=True)
class Star:
    span: Span
    body: object


@dataclass(frozen=True)
class Group:
    """带标签捕获组，index 从 1 开始。"""
    span: Span
    index: int
    body: object
