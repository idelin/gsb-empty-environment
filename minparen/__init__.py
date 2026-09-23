"""minparen：可配置优先级/结合性的表达式解析、最小括号打印与精确有理数求值。

解析器（parser）与打印器（printer）相互独立；打印目标以语法树结构相等
定义：``parse(to_string(t)) == t``，且删去任意一对输出括号都会使解析
失败或得到结构不同的树。
"""
from .ast_nodes import Binary, Call, Cond, Const, Node, Unary, Var
from .evaluate import EvalError, evaluate
from .operators import (DEFAULT_TABLE, LEFT, NONE, RIGHT, BinaryOp,
                        OperatorTable, TableError, UnaryOp)
from .parser import ParseError, Parser, parse, tokenize
from .printer import Rendered, format_const, render, to_string

__all__ = [
    "Node", "Const", "Var", "Unary", "Binary", "Cond", "Call",
    "OperatorTable", "BinaryOp", "UnaryOp", "TableError",
    "DEFAULT_TABLE", "LEFT", "RIGHT", "NONE",
    "Parser", "parse", "tokenize", "ParseError",
    "render", "to_string", "format_const", "Rendered",
    "evaluate", "EvalError",
]
