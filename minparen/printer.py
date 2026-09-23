"""独立的最小括号打印器。不调用解析器。

目标（以语法树结构定义，而非数值等价）
--------------------------------------
对任意适用域内的树 ``t``：

1. 结构往返：``parse(to_string(t)) == t``（结构相等）；
2. 最小性：删去输出中的任意一对括号，重新解析要么失败，要么得到
   结构不同的树。

括号规则及适用域
----------------
每个节点在一个"阈值"（上下文允许的最小优先级）下打印；
**节点优先级 < 阈值 ⇒ 必须加括号**，否则不加。各位置阈值：

- 二元 op（优先级 p）：
    - left 结合：左操作数阈值 p，右操作数阈值 p+1
      （故 ``a-(b-c)`` 的右操作数必须带括号，不会打印成 ``a-b-c``）；
    - right 结合（如 ``^``）：左操作数阈值 p+1，右操作数阈值 p
      （故 ``a^(b^c)`` 无需括号，``(a^b)^c`` 必须带括号）；
    - none 结合：两侧阈值均为 p+1（同层重复出现本来就会解析失败）。
- 一元前缀 op（优先级 q）：操作数阈值 q。
  适用域：解析器仅在一元优先级 >= 当前最小优先级处接受前缀一元运算，
  二者严格对应——所以 ``-x^2`` 无需括号而 ``(-x)^2``、``2^(-x)`` 必须带括号。
- 条件表达式（优先级 0，右结合）：条件阈值 1（左侧同层需括号），
  then 分支阈值 0（``?`` 与 ``:`` 之间是完整表达式），else 分支阈值 0。
- 函数调用 / 常量 / 变量：原子，优先级视为无穷大，任何上下文都不需要
  括号；调用参数阈值为 0。
- 常量打印适用域：**非负且可表示为有限小数的有理数**（分母仅含因子
  2 和 5）。其它常量（负数、如 1/3）无法用字面量无损表示，打印器抛出
  ``ValueError``。负常量的规范表示是 ``Unary('-', Const(...))``。

``render`` 额外返回每对括号的位置与理由（"每步变换说明适用域"）。
"""
from __future__ import annotations

from fractions import Fraction
from typing import NamedTuple

from .ast_nodes import Binary, Call, Cond, Const, Node, Unary, Var
from .operators import DEFAULT_TABLE, LEFT, NONE, RIGHT, OperatorTable

_ATOM_PRECEDENCE = 10 ** 9  # 原子（常量/变量/调用）视为无限优先级


class Rendered(NamedTuple):
    text: str
    parens: tuple  # tuple[(open_index, close_index), ...] 仅分组括号
    reasons: tuple  # tuple[str, ...] 每对括号的理由（适用域说明）


def _precedence(node: Node, table: OperatorTable) -> int:
    if isinstance(node, (Const, Var, Call)):
        return _ATOM_PRECEDENCE
    if isinstance(node, Unary):
        return table.unary[node.op].precedence
    if isinstance(node, Binary):
        return table.binary[node.op].precedence
    if isinstance(node, Cond):
        return table.cond_precedence
    raise TypeError(f"未知节点类型: {type(node).__name__}")


def _describe(node: Node) -> str:
    if isinstance(node, Const):
        return f"常量 {node.value}"
    if isinstance(node, Var):
        return f"变量 {node.name}"
    if isinstance(node, Unary):
        return f"一元运算 {node.op!r}"
    if isinstance(node, Binary):
        return f"二元运算 {node.op!r}"
    if isinstance(node, Cond):
        return "条件表达式 ?:"
    if isinstance(node, Call):
        return f"调用 {node.name}(...)"
    return repr(node)


def format_const(value: Fraction) -> str:
    """把非负有限小数有理数格式化为精确字面量（如 1/2 -> "0.5"）。

    适用域：value >= 0 且分母仅含因子 2、5；否则抛 ValueError。
    """
    if value < 0:
        raise ValueError(
            "打印器适用域：常量须非负；负常量请表示为 Unary('-', Const(...))"
        )
    num, den = value.numerator, value.denominator
    if den == 1:
        return str(num)
    d, twos, fives = den, 0, 0
    while d % 2 == 0:
        d //= 2
        twos += 1
    while d % 5 == 0:
        d //= 5
        fives += 1
    if d != 1:
        raise ValueError(
            f"打印器适用域：{value} 无有限小数展开，无法作为字面量精确打印"
        )
    places = max(twos, fives)
    scaled = num * (10 ** places) // den  # 整除，精确
    digits = str(scaled).rjust(places + 1, "0")
    head, tail = digits[:-places], digits[-places:].rstrip("0")
    return head if not tail else f"{head}.{tail}"


class _Emitter:
    def __init__(self) -> None:
        self.parts: list[str] = []
        self.pos = 0
        self.parens: list[tuple[int, int]] = []
        self.reasons: list[str] = []

    def emit(self, s: str) -> None:
        self.parts.append(s)
        self.pos += len(s)


def _fmt(node: Node, threshold: int, ctx: str,
         table: OperatorTable, em: _Emitter) -> None:
    prec = _precedence(node, table)
    wrap = prec < threshold
    if wrap:
        open_pos = em.pos
        em.emit("(")
    if isinstance(node, Const):
        em.emit(format_const(node.value))
    elif isinstance(node, Var):
        em.emit(node.name)
    elif isinstance(node, Unary):
        uop = table.unary[node.op]
        em.emit(node.op)
        _fmt(node.operand, uop.precedence,
             f"一元运算符 {node.op!r} 的操作数（阈值 = 其优先级 {uop.precedence}）",
             table, em)
    elif isinstance(node, Binary):
        op = table.binary[node.op]
        p = op.precedence
        if op.assoc == LEFT:
            lt, rt = p, p + 1
        elif op.assoc == RIGHT:
            lt, rt = p + 1, p
        else:  # NONE
            lt = rt = p + 1
        _fmt(node.left, lt,
             f"{op.assoc} 结合运算符 {node.op!r}（优先级 {p}）的左操作数，阈值 {lt}",
             table, em)
        em.emit(f" {node.op} ")
        _fmt(node.right, rt,
             f"{op.assoc} 结合运算符 {node.op!r}（优先级 {p}）的右操作数，阈值 {rt}",
             table, em)
    elif isinstance(node, Cond):
        cp = table.cond_precedence
        _fmt(node.cond, cp + 1,
             "条件表达式的条件（?: 右结合，左侧同层必须加括号）", table, em)
        em.emit(" ? ")
        _fmt(node.then, 0,
             "条件表达式的 then 分支（? 与 : 之间是完整表达式，阈值 0）",
             table, em)
        em.emit(" : ")
        _fmt(node.otherwise, cp,
             "条件表达式的 else 分支（右结合，阈值 0）", table, em)
    elif isinstance(node, Call):
        em.emit(node.name)
        em.emit("(")
        for k, arg in enumerate(node.args):
            if k:
                em.emit(", ")
            _fmt(arg, 0, f"函数 {node.name} 的第 {k + 1} 个参数（阈值 0）",
                 table, em)
        em.emit(")")
    else:
        raise TypeError(f"未知节点类型: {type(node).__name__}")
    if wrap:
        close_pos = em.pos
        em.emit(")")
        em.parens.append((open_pos, close_pos))
        em.reasons.append(
            f"为[{_describe(node)}]加括号：其优先级 {prec} < 上下文阈值 "
            f"{threshold}（{ctx}）"
        )


def render(node: Node, table: OperatorTable = DEFAULT_TABLE) -> Rendered:
    """打印并返回文本、分组括号位置与每对括号的理由。"""
    em = _Emitter()
    _fmt(node, 0, "顶层表达式（阈值 0）", table, em)
    return Rendered("".join(em.parts), tuple(em.parens), tuple(em.reasons))


def to_string(node: Node, table: OperatorTable = DEFAULT_TABLE) -> str:
    """打印为最少必要括号的表达式串。"""
    return render(node, table).text
