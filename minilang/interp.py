"""Hand-written interpreter for the IR. No eval/exec anywhere.

Division and modulo truncate toward zero (C-style), so results are
platform-independent. Runtime faults raise Trap and are reported in the
RunResult so the differential checker can compare failing behaviours too.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .ir import IRProgram

DEFAULT_FUEL = 100_000


class Trap(Exception):
    """Runtime fault: div-by-zero, stack underflow, bad pc, fuel exhausted..."""


def trunc_div(a: int, b: int) -> int:
    if b == 0:
        raise Trap("division by zero")
    q = abs(a) // abs(b)
    return q if (a < 0) == (b < 0) else -q


def trunc_mod(a: int, b: int) -> int:
    if b == 0:
        raise Trap("modulo by zero")
    return a - trunc_div(a, b) * b


@dataclass
class RunResult:
    outputs: tuple[int, ...]
    variables: dict[str, int]
    error: str | None
    steps: int

    def observable(self) -> tuple:
        """The behaviour the differential checker compares."""
        return (self.outputs, self.error, tuple(sorted(self.variables.items())))


def run(prog: IRProgram, inputs: tuple[int, ...] | list[int], fuel: int = DEFAULT_FUEL) -> RunResult:
    stack: list[int] = []
    variables: dict[str, int] = {}
    outputs: list[int] = []
    pc = 0
    steps = 0
    error: str | None = None
    instrs = prog.instrs

    def pop() -> int:
        if not stack:
            raise Trap("stack underflow")
        return stack.pop()

    try:
        while True:
            if steps >= fuel:
                raise Trap(f"fuel exhausted after {fuel} steps")
            if not 0 <= pc < len(instrs):
                raise Trap(f"pc {pc} out of range [0, {len(instrs)})")
            ins = instrs[pc]
            steps += 1
            op = ins.op
            if op == "HALT":
                break
            elif op == "CONST":
                stack.append(ins.arg)
            elif op == "LOAD":
                name = ins.arg
                if name not in variables:
                    raise Trap(f"undefined variable {name!r}")
                stack.append(variables[name])
            elif op == "STORE":
                variables[ins.arg] = pop()
            elif op == "INPUT":
                idx = ins.arg
                if not 0 <= idx < len(inputs):
                    raise Trap(f"input index {idx} out of range ({len(inputs)} given)")
                stack.append(inputs[idx])
            elif op == "OUTPUT":
                outputs.append(pop())
            elif op == "NEG":
                stack.append(-pop())
            elif op in ("ADD", "SUB", "MUL", "DIV", "MOD"):
                b, a = pop(), pop()
                if op == "ADD":
                    stack.append(a + b)
                elif op == "SUB":
                    stack.append(a - b)
                elif op == "MUL":
                    stack.append(a * b)
                elif op == "DIV":
                    stack.append(trunc_div(a, b))
                else:
                    stack.append(trunc_mod(a, b))
            elif op in ("EQ", "NE", "LT", "LE", "GT", "GE"):
                b, a = pop(), pop()
                result = {
                    "EQ": a == b, "NE": a != b, "LT": a < b,
                    "LE": a <= b, "GT": a > b, "GE": a >= b,
                }[op]
                stack.append(1 if result else 0)
            elif op == "JMP":
                pc = ins.arg
                continue
            elif op == "JZ":
                cond = pop()
                if cond == 0:
                    pc = ins.arg
                    continue
            else:  # pragma: no cover - validate() catches this earlier
                raise Trap(f"unknown opcode {op!r}")
            pc += 1
    except Trap as exc:
        error = str(exc)
    return RunResult(tuple(outputs), variables, error, steps)
