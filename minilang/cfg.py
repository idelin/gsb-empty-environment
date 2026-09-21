"""控制流图构建。

每个简单语句（赋值 / print）一个结点，每个 if / while 的条件一个结点。
条件为编译期常量时做常量折叠：不可走的边不连，从而产生不可达语句。
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .nodes import Assign, Bin, Expr, If, Neg, Num, Print, Program, Stmt, Var, While


@dataclass
class Block:
    id: int
    kind: str                       # 'entry' | 'exit' | 'assign' | 'print' | 'cond'
    span: tuple[int, int] | None = None
    stmt: Stmt | None = None        # kind 为 assign / print 时指向语句
    cond: Expr | None = None        # kind 为 cond 时指向条件表达式
    succs: list[Block] = field(default_factory=list)
    preds: list[Block] = field(default_factory=list)

    def label(self) -> str:
        return f"b{self.id}"


@dataclass
class CFG:
    entry: Block
    exit: Block
    blocks: list[Block]


class _NotConst(Exception):
    pass


def _fold(expr: Expr) -> int:
    """常量折叠；含变量或除零时抛 _NotConst。"""
    if isinstance(expr, Num):
        return expr.value
    if isinstance(expr, Var):
        raise _NotConst
    if isinstance(expr, Neg):
        return -_fold(expr.operand)
    if isinstance(expr, Bin):
        left = _fold(expr.left)
        right = _fold(expr.right)
        if expr.op == "+":
            return left + right
        if expr.op == "-":
            return left - right
        if expr.op == "*":
            return left * right
        if expr.op in ("/", "%"):
            if right == 0:
                raise _NotConst
            quot = abs(left) // abs(right)
            quot = quot if (left < 0) == (right < 0) else -quot
            return quot if expr.op == "/" else left - quot * right
        if expr.op == "==":
            return int(left == right)
        if expr.op == "!=":
            return int(left != right)
        if expr.op == "<":
            return int(left < right)
        if expr.op == "<=":
            return int(left <= right)
        if expr.op == ">":
            return int(left > right)
        if expr.op == ">=":
            return int(left >= right)
    raise _NotConst


def const_value(expr: Expr) -> bool | None:
    """条件为常量时返回 True/False，否则返回 None。"""
    try:
        return _fold(expr) != 0
    except _NotConst:
        return None


class CFGBuilder:
    def __init__(self):
        self.blocks: list[Block] = []

    def _new(self, kind: str, span=None, stmt=None, cond=None) -> Block:
        block = Block(len(self.blocks), kind, span, stmt, cond)
        self.blocks.append(block)
        return block

    @staticmethod
    def _link(src: Block, dst: Block) -> None:
        src.succs.append(dst)
        dst.preds.append(src)

    def build(self, program: Program) -> CFG:
        entry = self._new("entry")
        exit_ = self._new("exit")
        first = self._build_stmts(program.body, exit_)
        self._link(entry, first)
        return CFG(entry, exit_, self.blocks)

    def _build_stmts(self, stmts: list[Stmt], succ: Block) -> Block:
        for stmt in reversed(stmts):
            succ = self._build_stmt(stmt, succ)
        return succ

    def _build_stmt(self, stmt: Stmt, succ: Block) -> Block:
        if isinstance(stmt, Assign):
            block = self._new("assign", stmt.span, stmt=stmt)
            self._link(block, succ)
            return block
        if isinstance(stmt, Print):
            block = self._new("print", stmt.span, stmt=stmt)
            self._link(block, succ)
            return block
        if isinstance(stmt, If):
            cond_block = self._new("cond", stmt.cond.span, cond=stmt.cond)
            then_entry = self._build_stmts(stmt.then_body, succ)
            else_entry = self._build_stmts(stmt.else_body, succ) if stmt.else_body else succ
            cv = const_value(stmt.cond)
            if cv is not False:          # 常量假：不走 then
                self._link(cond_block, then_entry)
            if cv is not True:           # 常量真：不走 else / 不跳过
                self._link(cond_block, else_entry)
            return cond_block
        if isinstance(stmt, While):
            cond_block = self._new("cond", stmt.cond.span, cond=stmt.cond)
            body_entry = self._build_stmts(stmt.body, cond_block)  # 回边
            cv = const_value(stmt.cond)
            if cv is not False:          # 常量假：循环体不可达
                self._link(cond_block, body_entry)
            if cv is not True:           # 常量真：无出口边
                self._link(cond_block, succ)
            return cond_block
        raise TypeError(f"未知语句类型: {type(stmt).__name__}")
