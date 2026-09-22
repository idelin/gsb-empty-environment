from .errors import CompileError, Span
from .parser import parse
from . import ast_nodes as ast
from .bytecode import (
    Label, Instr, BytecodeModule, assemble, OP_BY_NAME,
)


def _op(name):
    return OP_BY_NAME[name]


BINOP_OPS = {
    "+": "ADD", "-": "SUB", "*": "MUL", "/": "DIV", "%": "MOD",
    "==": "EQ", "!=": "NE", "<": "LT", ">": "GT", "<=": "LE", ">=": "GE",
}


class Compiler:
    def __init__(self):
        self.instructions = []
        self.consts = []
        self.const_index = {}
        self.names = []
        self.name_index = {}
        self.break_stack = []

    def intern_const(self, value):
        key = ("bool" if isinstance(value, bool) else "int", value)
        if key not in self.const_index:
            self.const_index[key] = len(self.consts)
            self.consts.append(value)
        return self.const_index[key]

    def intern_name(self, name):
        if name not in self.name_index:
            self.name_index[name] = len(self.names)
            self.names.append(name)
        return self.name_index[name]

    def emit(self, name, operand=None, span=None):
        self.instructions.append(Instr(_op(name), operand, span))

    def new_label(self):
        return Label()

    def place(self, label):
        label.at = sum(1 + ins.arity for ins in self.instructions)

    def compile_program(self, program):
        self.compile_block(program)
        self.emit("HALT", span=Span(program.end, program.end))
        return assemble(self.instructions, self.consts, self.names)

    def compile_block(self, block):
        for stmt in block.stmts:
            self.compile_stmt(stmt)

    def compile_stmt(self, stmt):
        method = {
            ast.Let: self.st_let,
            ast.Assign: self.st_assign,
            ast.ExprStmt: self.st_expr,
            ast.Block: self.st_block,
            ast.If: self.st_if,
            ast.While: self.st_while,
            ast.Break: self.st_break,
        }[type(stmt)]
        method(stmt)

    def st_let(self, stmt):
        self.compile_expr(stmt.value)
        self.emit("STORE", self.intern_name(stmt.name),
                  Span(stmt.start, stmt.end))

    def st_assign(self, stmt):
        self.compile_expr(stmt.value)
        self.emit("STORE", self.intern_name(stmt.name),
                  Span(stmt.start, stmt.end))

    def st_expr(self, stmt):
        self.compile_expr(stmt.expr)
        self.emit("POP", span=Span(stmt.expr.end, stmt.expr.end))

    def st_block(self, stmt):
        self.compile_block(stmt)

    def st_if(self, stmt):
        end_label = self.new_label()
        else_label = self.new_label() if stmt.otherwise is not None else end_label
        self.compile_expr(stmt.cond)
        self.emit("JF", else_label, Span(stmt.cond.start, stmt.cond.end))
        self.compile_stmt(stmt.then)
        if stmt.otherwise is not None:
            self.emit("JUMP", end_label, Span(stmt.start, stmt.end))
            self.place(else_label)
            self.compile_stmt(stmt.otherwise)
        self.place(end_label)

    def st_while(self, stmt):
        top = self.new_label()
        end = self.new_label()
        self.place(top)
        self.compile_expr(stmt.cond)
        self.emit("JF", end, Span(stmt.cond.start, stmt.cond.end))
        self.break_stack.append(end)
        self.compile_stmt(stmt.body)
        self.break_stack.pop()
        self.emit("JUMP", top, Span(stmt.start, stmt.end))
        self.place(end)

    def st_break(self, stmt):
        if not self.break_stack:
            raise CompileError("break outside of a loop",
                               Span(stmt.start, stmt.end))
        self.emit("JUMP", self.break_stack[-1],
                  Span(stmt.start, stmt.end))

    def compile_expr(self, expr):
        method = {
            ast.IntLit: self.ex_int,
            ast.BoolLit: self.ex_bool,
            ast.Name: self.ex_name,
            ast.Unary: self.ex_unary,
            ast.Binary: self.ex_binary,
        }[type(expr)]
        method(expr)

    def ex_int(self, expr):
        self.emit("PUSH_CONST", self.intern_const(expr.value),
                  Span(expr.start, expr.end))

    def ex_bool(self, expr):
        self.emit("PUSH_TRUE" if expr.value else "PUSH_FALSE",
                  span=Span(expr.start, expr.end))

    def ex_name(self, expr):
        self.emit("LOAD", self.intern_name(expr.name),
                  Span(expr.start, expr.end))

    def ex_unary(self, expr):
        self.compile_expr(expr.operand)
        self.emit("NEG" if expr.op == "-" else "NOT",
                  span=Span(expr.start, expr.end))

    def ex_binary(self, expr):
        if expr.op == "and":
            self.compile_short_circuit(expr, when=False)
            return
        if expr.op == "or":
            self.compile_short_circuit(expr, when=True)
            return
        self.compile_expr(expr.left)
        self.compile_expr(expr.right)
        self.emit(BINOP_OPS[expr.op], span=Span(expr.start, expr.end))

    def compile_short_circuit(self, expr, when):
        # Both paths leave exactly one value on the stack.
        # when=True  (or):  jump (keeping left value) if left is truthy.
        # when=False (and): jump (keeping left value) if left is falsy.
        done = self.new_label()
        self.compile_expr(expr.left)
        self.emit("DUP", span=Span(expr.start, expr.end))
        self.emit("JUMPT" if when else "JF", done,
                  Span(expr.left.start, expr.left.end))
        self.emit("POP", span=Span(expr.left.end, expr.left.end))
        self.compile_expr(expr.right)
        self.place(done)


def compile_source(source):
    program = parse(source)
    return Compiler().compile_program(program)
