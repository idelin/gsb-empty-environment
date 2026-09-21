"""栈机小语言: 自实现词法/语法分析、编译、验证与解释执行。"""
from __future__ import annotations

from . import isa
from .compiler import CompileError, Ins, assemble, compile_program
from .lexer import LexError, Token, tokenize
from .parser import ParseError, parse
from .verifier import VerifyError, verify
from .vm import BudgetExhausted, Machine, VMError


def build(src: str) -> bytes:
    """源码 -> 字节码, 并通过静态验证。"""
    code = compile_program(parse(src))
    verify(code)
    return code


def run_source(
    src: str, budget: int | None = None, trace: bool = False
) -> tuple[list[int], Machine]:
    """编译、验证并运行; 返回 (输出列表, 机器实例)。"""
    code = build(src)
    machine = Machine(code, budget=budget, trace=trace)
    machine.run()
    return machine.output, machine


__all__ = [
    "isa",
    "Ins",
    "Machine",
    "Token",
    "assemble",
    "build",
    "BudgetExhausted",
    "CompileError",
    "LexError",
    "ParseError",
    "VMError",
    "VerifyError",
    "compile_program",
    "parse",
    "run_source",
    "tokenize",
    "verify",
]
