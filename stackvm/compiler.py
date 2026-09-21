"""把 AST 编译为栈机字节码。

短路表达式编译:
  a and b           a or b
    <a>               <a>
    DUP               DUP
    JZ   end          JZ   eval_b
    POP               JMP  end
    <b>            eval_b:
  end:                POP
                      <b>
                    end:
两条路径在汇合标签处的栈高度都恰好为 1, 由验证器检查。
"""
from __future__ import annotations

from dataclasses import dataclass

from . import isa
from .parser import (
    Assign,
    Bin,
    Bool,
    Break,
    Expr,
    ExprStmt,
    If,
    Let,
    Logic,
    Num,
    Print,
    Stmt,
    Unary,
    Var,
    While,
)


class CompileError(Exception):
    pass


BIN_OPS = {
    "+": "ADD",
    "-": "SUB",
    "*": "MUL",
    "/": "DIV",
    "==": "EQ",
    "!=": "NE",
    "<": "LT",
    "<=": "LE",
    ">": "GT",
    ">=": "GE",
}


@dataclass
class Ins:
    op: str
    arg: int | str | None = None  # str 表示尚未解析的标签
    span: tuple[int, int] | None = None  # 源码 UTF-8 字节区间


class Compiler:
    def __init__(self) -> None:
        self.ins: list[Ins] = []
        self.slots: dict[str, int] = {}
        self.break_stack: list[str] = []  # 各层循环的退出标签
        self.label_count = 0

    def new_label(self, hint: str) -> str:
        self.label_count += 1
        return f"{hint}_{self.label_count}"

    def emit(
        self,
        op: str,
        arg: int | str | None = None,
        span: tuple[int, int] | None = None,
    ) -> None:
        self.ins.append(Ins(op, arg, span))

    def slot_of(self, name: str, span: tuple[int, int]) -> int:
        if name not in self.slots:
            raise CompileError(f"字节 {span[0]}: 未定义的变量 {name!r}")
        return self.slots[name]

    def compile(self, stmts: list[Stmt]) -> None:
        for stmt in stmts:
            self.stmt(stmt)
        self.emit("HALT")

    def stmt(self, stmt: Stmt) -> None:
        span = (stmt.start, stmt.end)

        if isinstance(stmt, Let):
            self.expr(stmt.expr)
            if stmt.name not in self.slots:
                self.slots[stmt.name] = len(self.slots)
            self.emit("STORE", self.slots[stmt.name], span)

        elif isinstance(stmt, Assign):
            self.expr(stmt.expr)
            self.emit("STORE", self.slot_of(stmt.name, span), span)

        elif isinstance(stmt, Print):
            self.expr(stmt.expr)
            self.emit("PRINT", span=span)

        elif isinstance(stmt, ExprStmt):
            self.expr(stmt.expr)
            self.emit("POP", span=span)

        elif isinstance(stmt, If):
            else_label = self.new_label("ifelse")
            end_label = self.new_label("ifend")
            self.expr(stmt.cond)
            self.emit("JZ", else_label, span)
            for s in stmt.then:
                self.stmt(s)
            self.emit("JMP", end_label, span=span)
            self.emit("LABEL", else_label)
            for s in stmt.otherwise:
                self.stmt(s)
            self.emit("LABEL", end_label)

        elif isinstance(stmt, While):
            cond_label = self.new_label("while")
            end_label = self.new_label("wend")
            self.emit("LABEL", cond_label)
            self.expr(stmt.cond)
            self.emit("JZ", end_label, span)
            self.break_stack.append(end_label)
            for s in stmt.body:
                self.stmt(s)
            self.break_stack.pop()
            self.emit("JMP", cond_label, span=span)
            self.emit("LABEL", end_label)

        elif isinstance(stmt, Break):
            if not self.break_stack:
                raise CompileError(f"字节 {span[0]}: 'break' 不在循环内")
            self.emit("JMP", self.break_stack[-1], span)

        else:
            raise CompileError(f"未知语句: {stmt!r}")

    def expr(self, expr: Expr) -> None:
        span = (expr.start, expr.end)

        if isinstance(expr, Num):
            self.emit("PUSH", expr.value, span)

        elif isinstance(expr, Bool):
            self.emit("PUSH", 1 if expr.value else 0, span)

        elif isinstance(expr, Var):
            self.emit("LOAD", self.slot_of(expr.name, span), span)

        elif isinstance(expr, Bin):
            self.expr(expr.left)
            self.expr(expr.right)
            self.emit(BIN_OPS[expr.op], span=span)

        elif isinstance(expr, Unary):
            self.expr(expr.operand)
            self.emit("NOT" if expr.op == "not" else "NEG", span=span)

        elif isinstance(expr, Logic):
            end_label = self.new_label("logic")
            self.expr(expr.left)
            self.emit("DUP", span=span)
            if expr.op == "and":
                # 左值为假 -> 结果就是左值, 直接跳过右支
                self.emit("JZ", end_label, span=span)
            else:  # or
                eval_right = self.new_label("loreval")
                self.emit("JZ", eval_right, span=span)
                self.emit("JMP", end_label, span=span)
                self.emit("LABEL", eval_right)
            self.emit("POP", span=span)
            self.expr(expr.right)
            self.emit("LABEL", end_label)

        else:
            raise CompileError(f"未知表达式: {expr!r}")


def assemble(
    instructions: list[Ins],
) -> tuple[bytes, list[tuple[int, tuple[int, int] | None]]]:
    """两遍汇编: 先确定标签的字节偏移, 再编码。"""
    label_offsets: dict[str, int] = {}
    positioned: list[tuple[int, Ins]] = []
    pc = 0
    for ins in instructions:
        if ins.op == "LABEL":
            label_offsets[ins.arg] = pc
            continue
        positioned.append((pc, ins))
        pc += 1 + isa.BY_NAME[ins.op].operand_size

    code = bytearray()
    source_map: list[tuple[int, tuple[int, int] | None]] = []
    for offset, ins in positioned:
        arg = ins.arg
        if isinstance(arg, str):
            arg = label_offsets[arg]
        code += isa.encode(ins.op, arg)
        source_map.append((offset, ins.span))
    return bytes(code), source_map


def compile_program(stmts: list[Stmt]) -> bytes:
    compiler = Compiler()
    compiler.compile(stmts)
    code, _source_map = assemble(compiler.ins)
    return code
