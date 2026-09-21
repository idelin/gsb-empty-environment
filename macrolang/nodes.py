"""语法树节点与源码位置定义。

所有源码位置均为 UTF-8 字节区间 [start, end)，即对
``source.encode("utf-8")[start:end]`` 切片可还原对应源码文本。
"""
from __future__ import annotations

from dataclasses import dataclass, field


@dataclass
class Span:
    """UTF-8 字节区间，start 含、end 不含。"""

    start: int
    end: int

    def __str__(self) -> str:
        return f"{self.start}..{self.end}"


@dataclass
class ExpansionRecord:
    """一次宏展开留下的双重源码映射。

    call_span 指向宏调用处的字节区间，def_span 指向宏定义模板中
    对应节点的字节区间。
    """

    macro_name: str
    call_span: Span
    def_span: Span


@dataclass
class Node:
    """语法树节点基类。

    provenance 记录该节点经历过的宏展开（外层在前），
    每个条目同时给出调用点与定义点位置。
    """

    span: Span | None = None
    provenance: list[ExpansionRecord] = field(default_factory=list)


@dataclass
class IntLit(Node):
    value: int = 0


@dataclass
class StrLit(Node):
    value: str = ""


@dataclass
class Sym(Node):
    name: str = ""


@dataclass
class Form(Node):
    items: list[Node] = field(default_factory=list)


def walk(node: Node):
    """先序遍历语法树，供源码映射导出与测试使用。"""
    yield node
    if isinstance(node, Form):
        for item in node.items:
            yield from walk(item)


def describe(node: Node) -> str:
    """节点的简短可读描述。"""
    if isinstance(node, IntLit):
        return f"int {node.value}"
    if isinstance(node, StrLit):
        return f"string {node.value!r}"
    if isinstance(node, Sym):
        return f"sym {node.name}"
    if isinstance(node, Form):
        head = ""
        if node.items and isinstance(node.items[0], Sym):
            head = node.items[0].name
        return f"form ({head} ...)" if head else "form (...)"
    return type(node).__name__
