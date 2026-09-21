"""错误类型:词法/语法错误、宏展开错误、求值错误。"""
from __future__ import annotations


class MacroLangError(Exception):
    def __init__(self, message: str, span=None):
        super().__init__(message)
        self.message = message
        self.span = span


class ParseError(MacroLangError):
    """词法或语法错误。"""


class ExpansionError(MacroLangError):
    """宏展开错误(参数个数、模板形状等)。"""


class EvalError(MacroLangError):
    """解释执行错误。"""


class ExpansionLimitError(ExpansionError):
    """展开深度或节点数超限,携带完整展开链。"""

    def __init__(self, kind: str, limit: int, chain):
        self.kind = kind
        self.limit = limit
        self.chain = tuple(chain)
        label = "depth" if kind == "depth" else "node-count"
        lines = [
            f"macro expansion {label} limit ({limit}) exceeded",
            "expansion chain (outermost first):",
        ]
        for i, frame in enumerate(self.chain, 1):
            lines.append(
                f"  {i}. macro '{frame.macro_name}'"
                f" called at bytes {frame.call_span.start}-{frame.call_span.end}"
                f" (defined at bytes {frame.def_span.start}-{frame.def_span.end})"
            )
        span = self.chain[-1].call_span if self.chain else None
        super().__init__("\n".join(lines), span=span)
