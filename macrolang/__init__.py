"""macrolang：基于语法树的卫生参数化宏与解释执行。

流水线：lex -> parse -> expand（卫生宏展开）-> interpret（树遍历求值）。
全程不使用 eval/exec/compile，源码位置一律为 UTF-8 字节区间。
"""
from .errors import (
    ArityError,
    DepthLimitExceeded,
    EvalError,
    LexError,
    MacroExpansionError,
    MacroLangError,
    NodeLimitExceeded,
    ParseError,
)
from .expander import Expander, ExpansionContext, Macro
from .interpreter import Env, Interpreter
from .lexer import lex
from .nodes import (
    ExpansionRecord,
    Form,
    IntLit,
    Node,
    Span,
    StrLit,
    Sym,
    describe,
    walk,
)
from .parser import parse

__all__ = [
    "ArityError",
    "DepthLimitExceeded",
    "Env",
    "EvalError",
    "Expander",
    "ExpansionContext",
    "ExpansionRecord",
    "Form",
    "IntLit",
    "Interpreter",
    "LexError",
    "Macro",
    "MacroExpansionError",
    "MacroLangError",
    "Node",
    "NodeLimitExceeded",
    "ParseError",
    "Span",
    "StrLit",
    "Sym",
    "describe",
    "expand_source",
    "lex",
    "parse",
    "run_source",
    "walk",
]


def expand_source(source: str, max_depth: int = 64, max_nodes: int = 100_000):
    """解析并展开源码，返回 (展开后的顶层形式列表, 展开器)。"""
    expander = Expander(max_depth=max_depth, max_nodes=max_nodes)
    forms = expander.expand_program(parse(source))
    return forms, expander


def run_source(
    source: str,
    out=None,
    max_depth: int = 64,
    max_nodes: int = 100_000,
):
    """解析、展开并执行源码，返回最后一个顶层形式的值。"""
    forms, _ = expand_source(source, max_depth=max_depth, max_nodes=max_nodes)
    return Interpreter(out=out).run(forms)
