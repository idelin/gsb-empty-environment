"""stacklang: a small language compiled to a validated stack machine.

Public API:
  compile_source(source) -> BytecodeModule
  validate(module)       -> True (raises ValidationError)
  VM(module, env=None)   -> machine with step()/run(budget=...)
  execute(source, budget=None) -> (env, steps)
  disassemble(module)    -> str
"""

from .errors import (
    StackLangError, LexError, ParseError, CompileError,
    ValidationError, RuntimeErr, BudgetExceeded,
)
from .bytecode import BytecodeModule, OPCODES, decode
from .compiler import compile_source
from .validator import validate
from .vm import VM

__all__ = [
    "compile_source", "validate", "VM", "execute", "disassemble",
    "BytecodeModule", "StackLangError", "LexError", "ParseError",
    "CompileError", "ValidationError", "RuntimeErr", "BudgetExceeded",
]


def execute(source, budget=None, env=None):
    module = compile_source(source)
    machine = VM(module, env=env)
    machine.run(budget=budget)
    return machine.env, machine.steps


def disassemble(module):
    instructions, _ = decode(module)
    lines = []
    for ins in instructions:
        name = ins.name
        text = f"{ins.offset:04d}  {name}"
        if name == "PUSH_CONST":
            text += f" {ins.operand}  ; {module.consts[ins.operand]!r}"
        elif name in ("LOAD", "STORE"):
            text += f" {ins.operand}  ; {module.names[ins.operand]}"
        elif ins.operand is not None:
            text += f" {ins.operand}"
        lines.append(text)
    return "\n".join(lines)
