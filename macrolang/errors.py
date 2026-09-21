"""宏语言的错误类型。

所有错误信息中的源码位置均为 UTF-8 字节区间。
"""
from __future__ import annotations

from .nodes import Span


class MacroLangError(Exception):
    """所有宏语言错误的基类。"""

    def __init__(self, message: str, span: Span | None = None):
        self.span = span
        if span is not None:
            message = f"{message} (at bytes {span})"
        super().__init__(message)


class LexError(MacroLangError):
    """词法错误。"""


class ParseError(MacroLangError):
    """语法错误。"""


class EvalError(MacroLangError):
    """解释执行期错误。"""


class MacroExpansionError(MacroLangError):
    """宏展开期错误，携带完整展开链。

    chain 为 (宏名, 调用点字节区间) 的列表，外层在前。
    """

    def __init__(self, message: str, chain=(), span: Span | None = None):
        self.chain = list(chain)
        head = message
        if span is not None:
            head = f"{head} (at bytes {span})"
        parts = [head]
        if self.chain:
            parts.append("Expansion chain (outermost first):")
            for index, (name, call_span) in enumerate(self.chain, 1):
                parts.append(f"  #{index} {name} called at bytes {call_span}")
        super().__init__("\n".join(parts))


class DepthLimitExceeded(MacroExpansionError):
    """宏展开深度超限。"""

    def __init__(self, max_depth: int, chain):
        super().__init__(
            f"macro expansion depth limit ({max_depth}) exceeded", chain
        )
        self.max_depth = max_depth


class NodeLimitExceeded(MacroExpansionError):
    """宏展开产生节点数超限。"""

    def __init__(self, max_nodes: int, chain):
        super().__init__(
            f"macro expansion node limit ({max_nodes}) exceeded", chain
        )
        self.max_nodes = max_nodes


class ArityError(MacroExpansionError):
    """宏实参与形参数量不匹配。"""

    def __init__(self, macro_name: str, expected: int, got: int, chain, span):
        super().__init__(
            f"macro {macro_name!r} expects {expected} argument(s), got {got}",
            chain,
            span,
        )
