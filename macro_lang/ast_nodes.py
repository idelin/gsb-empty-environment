"""AST 节点定义。所有源码位置均为 UTF-8 字节区间 [start, end)。"""
from __future__ import annotations

import json
from dataclasses import dataclass, field


@dataclass(frozen=True)
class Span:
    """UTF-8 字节区间,start 含、end 不含。"""

    start: int
    end: int


@dataclass
class Origin:
    """节点的来源信息,实现"调用处 + 定义处"双重源码映射。

    kind == "source": 直接来自用户源码,span 即其位置。
    kind == "macro":  由宏展开生成,def_span 指向宏定义中的模板位置,
                      call_span 指向宏调用处。
    """

    kind: str = "source"
    macro_name: str | None = None
    def_span: Span | None = None
    call_span: Span | None = None


@dataclass
class Node:
    span: Span
    origin: Origin = field(default_factory=Origin)


@dataclass
class Symbol(Node):
    name: str = ""


@dataclass
class Number(Node):
    value: int | float = 0


@dataclass
class Text(Node):
    value: str = ""


@dataclass
class Call(Node):
    items: list[Node] = field(default_factory=list)


def clone_node(node: Node) -> Node:
    """深拷贝,保留 span 与 origin(用于把实参 AST 代入宏体)。"""
    if isinstance(node, Call):
        return Call(node.span, node.origin, [clone_node(i) for i in node.items])
    if isinstance(node, Symbol):
        return Symbol(node.span, node.origin, node.name)
    if isinstance(node, Number):
        return Number(node.span, node.origin, node.value)
    if isinstance(node, Text):
        return Text(node.span, node.origin, node.value)
    raise TypeError(f"unknown node: {node!r}")


def subtree_size(node: Node) -> int:
    if isinstance(node, Call):
        return 1 + sum(subtree_size(i) for i in node.items)
    return 1


def render(node: Node) -> str:
    if isinstance(node, Symbol):
        return node.name
    if isinstance(node, Number):
        return repr(node.value)
    if isinstance(node, Text):
        return json.dumps(node.value, ensure_ascii=False)
    if isinstance(node, Call):
        return "(" + " ".join(render(i) for i in node.items) + ")"
    raise TypeError(f"unknown node: {node!r}")
