"""minilang:带括号、调用与声明的小语言。

- 全量 / 增量解析,按编辑范围复用未变语法子树
- 错误恢复产生明确的 ErrorNode,未闭合括号不阻碍后续声明
- 源码位置一律使用 UTF-8 字节区间,随编辑自动更新
- 自实现词法、语法与解释执行,不依赖 eval 或现成编译器
"""

from .document import Document
from .interpreter import EvalError, evaluate
from .lexer import Token, lex
from .nodes import (Binary, Call, ErrorNode, FuncDecl, Ident, LetDecl, Node,
                    Number, Program, Unary, compute_positions, tree_signature,
                    walk)
from .parser import Parser, parse, parse_text

__all__ = [
    "Binary", "Call", "Document", "ErrorNode", "EvalError", "FuncDecl",
    "Ident", "LetDecl", "Node", "Number", "Parser", "Program", "Token",
    "Unary", "compute_positions", "evaluate", "lex", "parse", "parse_text",
    "tree_signature", "walk",
]
