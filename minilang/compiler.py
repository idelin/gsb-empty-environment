"""AST -> IR compiler with back-patched jump targets."""

from __future__ import annotations

from . import ast_nodes as ast
from .ir import Instr, IRProgram

BINOPS = {
    "+": "ADD", "-": "SUB", "*": "MUL", "/": "DIV", "%": "MOD",
    "==": "EQ", "!=": "NE", "<": "LT", "<=": "LE", ">": "GT", ">=": "GE",
}


class Compiler:
    def __init__(self):
        self.code: list[Instr] = []
        self.num_inputs = 0

    def emit(self, op: str, arg=None) -> int:
        self.code.append(Instr(op, arg))
        return len(self.code) - 1

    def patch(self, pos: int, target: int) -> None:
        self.code[pos] = Instr(self.code[pos].op, target)

    def compile_program(self, program: ast.Program) -> IRProgram:
        for stmt in program.body:
            self.compile_stmt(stmt)
        self.emit("HALT")
        return IRProgram(self.code, self.num_inputs)

    def compile_stmt(self, stmt: ast.Stmt) -> None:
        if isinstance(stmt, ast.Let):
            self.compile_expr(stmt.expr)
            self.emit("STORE", stmt.name)
        elif isinstance(stmt, ast.Assign):
            self.compile_expr(stmt.expr)
            self.emit("STORE", stmt.name)
        elif isinstance(stmt, ast.Output):
            self.compile_expr(stmt.expr)
            self.emit("OUTPUT")
        elif isinstance(stmt, ast.If):
            self.compile_expr(stmt.cond)
            jz = self.emit("JZ", None)
            for s in stmt.then_body:
                self.compile_stmt(s)
            if stmt.else_body:
                jmp = self.emit("JMP", None)
                self.patch(jz, len(self.code))
                for s in stmt.else_body:
                    self.compile_stmt(s)
                self.patch(jmp, len(self.code))
            else:
                self.patch(jz, len(self.code))
        elif isinstance(stmt, ast.While):
            loop_start = len(self.code)
            self.compile_expr(stmt.cond)
            jz = self.emit("JZ", None)
            for s in stmt.body:
                self.compile_stmt(s)
            self.emit("JMP", loop_start)
            self.patch(jz, len(self.code))
        else:  # pragma: no cover - defensive
            raise TypeError(f"unknown statement: {stmt!r}")

    def compile_expr(self, expr: ast.Expr) -> None:
        if isinstance(expr, ast.IntLit):
            self.emit("CONST", expr.value)
        elif isinstance(expr, ast.Var):
            self.emit("LOAD", expr.name)
        elif isinstance(expr, ast.Input):
            self.num_inputs = max(self.num_inputs, expr.index + 1)
            self.emit("INPUT", expr.index)
        elif isinstance(expr, ast.Neg):
            self.compile_expr(expr.operand)
            self.emit("NEG")
        elif isinstance(expr, ast.BinOp):
            self.compile_expr(expr.left)
            self.compile_expr(expr.right)
            self.emit(BINOPS[expr.op])
        else:  # pragma: no cover - defensive
            raise TypeError(f"unknown expression: {expr!r}")


def compile(program: ast.Program) -> IRProgram:
    return Compiler().compile_program(program)
