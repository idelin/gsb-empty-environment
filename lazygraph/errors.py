"""错误类型。

- 词法/语法错误在编译期抛出，携带 UTF-8 字节区间。
- 运行期错误（除零、未绑定变量）是值的一部分：一旦在 thunk 中
  产生，就和正常值一样被缓存，再次引用同一 thunk 直接重抛同一异常。
- 黑洞错误表示求值过程中强制了正在求值的 thunk（直接/间接自引用），
  异常对象上带有触发处的强制链。
"""
from __future__ import annotations

from dataclasses import dataclass

from .sourcemap import SourceMap, Span


class LazyError(Exception):
    """所有自定义错误的基类。"""

    def __init__(self, message: str, span: Span | None = None) -> None:
        super().__init__(message)
        self.message = message
        self.span = span

    def render(self, sourcemap: SourceMap | None) -> str:
        if self.span is not None and sourcemap is not None:
            return f"{self.message}\n{sourcemap.format_span(self.span)}"
        location = ""
        if self.span is not None:
            location = f" (字节区间 {self.span.start}..{self.span.end})"
        return f"{self.message}{location}"


class LexError(LazyError):
    """词法错误：非法字符、未终止的注释等。"""


class ParseError(LazyError):
    """语法错误。"""


class EvalError(LazyError):
    """运行期错误的基类（会被 thunk 缓存）。"""


class UnboundVariableError(EvalError):
    """引用了不存在的变量。"""


class DivByZeroError(EvalError):
    """``/`` 或 ``%`` 的除数为 0。"""


@dataclass
class ChainEntry:
    """强制求值链上的一环：某个 thunk 在 ``at`` 处被强制。"""

    name: str
    binding_span: Span | None
    at: Span | None


class BlackHoleError(EvalError):
    """检测到自引用黑洞：强制一个正处于求值中的 thunk。

    ``cycle`` 按从最内层（重复出现的 thunk）到最外层的顺序保存强制链，
    例如间接环 ``a -> b -> a`` 对应 ``[a@引用处, b@引用处, a@回到处]``。
    """

    def __init__(self, thunk_name: str, chain: list[ChainEntry]) -> None:
        self.thunk_name = thunk_name
        self.chain: list[ChainEntry] = list(chain)
        names = " -> ".join(entry.name for entry in self.chain)
        message = (
            f"自引用黑洞：thunk '{thunk_name}' 在自身求值完成前又被强制"
            f"（环: {names}）"
        )
        first = chain[0]
        super().__init__(message, span=first.at if first.at else first.binding_span)

    def render_chain(self, sourcemap: SourceMap | None) -> str:
        lines = [self.message, "强制求值环（外 -> 内，最后一环回到自身）:"]
        for depth, entry in enumerate(self.chain):
            location = "<根表达式>"
            if entry.at is not None and sourcemap is not None:
                location = f"{sourcemap.describe(entry.at.start)}: {sourcemap.slice(entry.at)}"
            elif entry.binding_span is not None and sourcemap is not None:
                location = f"{sourcemap.describe(entry.binding_span.start)}（绑定处）"
            repeat = "  <-- 回到自身，检测到环" if depth == len(self.chain) - 1 else ""
            lines.append(f"  {'  ' * depth}- {entry.name}  @ {location}{repeat}")
        return "\n".join(lines)
