"""可配置的运算优先级 / 结合性表，并负责拒绝有歧义的表。

表的形式：``[(符号, 优先级, 结合性), ...]``，优先级为正整数，数字越大
越紧密（越靠近原子）。结合性为 ``"L"``（左结合）或 ``"R"``（右结合）。

歧义（构造时以 :class:`GrammarError` 拒绝）包括：

1. 符号重复或占用保留符号 ``? : ( ) ,``；
2. 优先级不是正整数、结合性非法；
3. 同一优先级混排左结合与右结合算符——否则 ``a OP b OP c`` 无法唯一
   确定分组；
4. 比一元负号更紧密的二元层若为左结合，一元负号与该层的归约次序会
   冲突（``-a~b`` 的分组不唯一），故该层只允许右结合（对应标准数学中
   幂 ``-x^y == -(x^y)`` 的约定）。
"""

from __future__ import annotations

from dataclasses import dataclass

from .errors import GrammarError

RESERVED_SYMBOLS = {"?", ":", "(", ")", ","}
DEFAULT_UNARY_SYMBOL = "-"
DEFAULT_UNARY_PREC = 3
DEFAULT_OPERATORS = [
    ("+", 1, "L"),
    ("-", 1, "L"),
    ("*", 2, "L"),
    ("/", 2, "L"),
    ("^", 4, "R"),
]


@dataclass(frozen=True)
class Operator:
    symbol: str
    prec: int
    assoc: str


@dataclass(frozen=True)
class Grammar:
    operators: tuple
    unary_symbol: str
    unary_prec: int

    @property
    def prec_of(self) -> dict:
        return {op.symbol: op.prec for op in self.operators}

    @property
    def assoc_of(self) -> dict:
        return {op.symbol: op.assoc for op in self.operators}

    @staticmethod
    def build(
        operators=DEFAULT_OPERATORS,
        unary_symbol: str = DEFAULT_UNARY_SYMBOL,
        unary_prec: int = DEFAULT_UNARY_PREC,
    ) -> "Grammar":
        if not isinstance(unary_symbol, str) or len(unary_symbol) != 1:
            raise GrammarError("一元算符必须是单个非空字符")
        if unary_symbol in RESERVED_SYMBOLS:
            raise GrammarError(f"一元算符 {unary_symbol!r} 是保留符号")
        if not isinstance(unary_prec, int) or unary_prec <= 0:
            raise GrammarError("一元算符优先级必须是正整数")

        seen = set()
        level_assoc = {}
        normalized = []
        for entry in operators:
            if len(entry) != 3:
                raise GrammarError(f"表项必须是 (符号, 优先级, 结合性)：{entry!r}")
            symbol, prec, assoc = entry
            if not isinstance(symbol, str) or not symbol:
                raise GrammarError("算符符号必须是非空字符串")
            if any(ch in RESERVED_SYMBOLS for ch in symbol):
                raise GrammarError(f"算符 {symbol!r} 含保留符号")
            if symbol in seen:
                raise GrammarError(f"算符 {symbol!r} 重复定义")
            seen.add(symbol)
            if not isinstance(prec, int) or isinstance(prec, bool) or prec <= 0:
                raise GrammarError(f"算符 {symbol!r} 的优先级必须是正整数")
            if assoc not in ("L", "R"):
                raise GrammarError(f"算符 {symbol!r} 的结合性必须是 'L' 或 'R'")
            if prec in level_assoc and level_assoc[prec] != assoc:
                raise GrammarError(
                    f"优先级 {prec} 同时声明了左结合与右结合，"
                    "'a OP b OP c' 分组不唯一"
                )
            level_assoc[prec] = assoc
            normalized.append(Operator(symbol, prec, assoc))

        for op in normalized:
            if op.prec > unary_prec and op.assoc == "L":
                raise GrammarError(
                    f"算符 {op.symbol!r} 比一元 {unary_symbol!r} 更紧密却为左结合，"
                    f"'-a {op.symbol} b' 存在归约歧义"
                )

        ordered = tuple(
            sorted(normalized, key=lambda o: (-len(o.symbol), o.symbol))
        )
        return Grammar(ordered, unary_symbol, unary_prec)
