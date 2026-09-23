"""可配置运算符表：优先级、结合性，以及歧义表的拒绝。

适用域与规则
------------
- 二元运算符：``符号 -> (优先级, 结合性)``，结合性 ∈ {left, right, none}。
- 一元前缀运算符：``符号 -> 优先级``（例如负号 ``-``）。
- 条件表达式 ``? :`` 与函数调用为内建语法，不在表中；条件表达式优先级
  固定为 0（最低），右结合。
- 优先级为正整数，数值越大结合越紧。

歧义拒绝（构造表时抛出 :class:`TableError`）
-------------------------------------------
优先级攀爬解析器在给定 (优先级, 结合性) 后解析结果唯一，但以下表本身
就承认两种解读，必须拒绝：

1. 同一优先级上的二元运算符结合性不一致
   （``a OP1 b OP2 c`` 既可左结合又可右结合，歧义）。
2. ``none``（不可结合）运算符与其它运算符共享优先级
   （连续出现时无法判定是报错还是结合，歧义）。

另外拒绝：非法结合性名称、非正整数优先级、空符号、与标识符/数字/保留
字符冲突的符号。
"""
from __future__ import annotations

import re
from dataclasses import dataclass

LEFT = "left"
RIGHT = "right"
NONE = "none"
ASSOCS = (LEFT, RIGHT, NONE)

_IDENT_RE = re.compile(r"[A-Za-z_][A-Za-z0-9_]*\Z")
_RESERVED_CHARS = set("()?,:.\"'")


class TableError(ValueError):
    """运算符表非法或存在歧义。"""


@dataclass(frozen=True)
class BinaryOp:
    symbol: str
    precedence: int
    assoc: str


@dataclass(frozen=True)
class UnaryOp:
    symbol: str
    precedence: int


def _check_symbol(sym: object) -> None:
    if not isinstance(sym, str) or not sym:
        raise TableError(f"运算符符号必须是非空字符串，得到 {sym!r}")
    if _IDENT_RE.match(sym):
        raise TableError(f"运算符符号 {sym!r} 是标识符，会与变量/函数名混淆")
    for ch in sym:
        if ch in _RESERVED_CHARS or ch.isspace() or ch.isdigit():
            raise TableError(f"运算符符号 {sym!r} 含保留字符/空白/数字")


def _check_precedence(prec: object) -> None:
    if not isinstance(prec, int) or isinstance(prec, bool) or prec < 1:
        raise TableError(f"优先级必须是 >= 1 的整数，得到 {prec!r}")


class OperatorTable:
    """运算符优先级/结合性配置表。构造时即完成全部歧义检查。"""

    #: 条件表达式 ``? :`` 的内建优先级（最低）。
    cond_precedence = 0

    def __init__(self, binary: dict, unary: dict) -> None:
        self.binary: dict[str, BinaryOp] = {}
        for sym, spec in binary.items():
            try:
                prec, assoc = spec
            except (TypeError, ValueError):
                raise TableError(
                    f"二元运算符 {sym!r} 的配置应为 (优先级, 结合性)，得到 {spec!r}"
                ) from None
            _check_symbol(sym)
            _check_precedence(prec)
            if assoc not in ASSOCS:
                raise TableError(
                    f"运算符 {sym!r} 的结合性 {assoc!r} 非法，应为 {ASSOCS} 之一"
                )
            self.binary[sym] = BinaryOp(sym, prec, assoc)

        self.unary: dict[str, UnaryOp] = {}
        for sym, prec in unary.items():
            _check_symbol(sym)
            _check_precedence(prec)
            self.unary[sym] = UnaryOp(sym, prec)

        # --- 歧义检查 -------------------------------------------------
        by_prec: dict[int, list[BinaryOp]] = {}
        for op in self.binary.values():
            by_prec.setdefault(op.precedence, []).append(op)
        for prec, ops in sorted(by_prec.items()):
            assocs = {op.assoc for op in ops}
            if len(assocs) > 1:
                names = ", ".join(sorted(op.symbol for op in ops))
                raise TableError(
                    f"歧义：优先级 {prec} 上的运算符（{names}）结合性不一致，"
                    "连用时次序无法确定"
                )
            if assocs == {NONE} and len(ops) > 1:
                names = ", ".join(sorted(op.symbol for op in ops))
                raise TableError(
                    f"歧义：none 结合性运算符（{names}）必须独占一个优先级"
                )

        #: 全部运算符符号，按长度降序（词法分析最长匹配用）。
        self.symbols = sorted(set(self.binary) | set(self.unary),
                              key=len, reverse=True)


#: 默认运算符表。
#: 负号优先级 3：低于幂 ``^``(4)，故 ``-x^2 == -(x^2)``，而 ``(-x)^2`` 需括号；
#: 高于 ``*`` ``/``(2)，故 ``-a*b == (-a)*b``。幂 ``^`` 右结合。
DEFAULT_TABLE = OperatorTable(
    binary={
        "+": (1, LEFT),
        "-": (1, LEFT),
        "*": (2, LEFT),
        "/": (2, LEFT),
        "^": (4, RIGHT),
    },
    unary={"-": 3},
)
