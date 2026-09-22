"""Stack machine runtime with single-stepping and an instruction budget."""

from .bytecode import BytecodeModule, decode, OP_BY_NAME
from .errors import RuntimeErr, BudgetExceeded
from .validator import validate


def _truthy(value):
    if isinstance(value, bool):
        return value
    return value != 0


def _trunc_div(a, b):
    # Integer division truncated toward zero, like C-style / on integers.
    q = abs(a) // abs(b)
    return q if (a < 0) == (b < 0) else -q


class VM:
    def __init__(self, module, env=None, validate_code=True):
        if validate_code:
            validate(module)
        self.module = module
        self.instructions, _ = decode(module)
        self.by_offset = {ins.offset: ins for ins in self.instructions}
        self.pc = 0
        self.stack = []
        self.env = dict(env or {})
        self.steps = 0
        self.halted = False

    def _span(self, ins):
        return self.module.spans.get(ins.offset)

    def _err(self, message, ins):
        return RuntimeErr(message, self._span(ins))

    def step(self):
        """Execute one instruction. Returns the executed instruction name."""
        if self.halted:
            raise RuntimeErr("machine has halted")
        ins = self.by_offset.get(self.pc)
        if ins is None:
            raise RuntimeErr(f"pc {self.pc} is not an instruction boundary")
        name = ins.name
        stack = self.stack

        if name == "HALT":
            self.pc = ins.next_offset
            self.halted = True
            return name
        if name == "PUSH_CONST":
            stack.append(self.module.consts[ins.operand])
            self.pc = ins.next_offset
        elif name in ("PUSH_TRUE", "PUSH_FALSE"):
            stack.append(name == "PUSH_TRUE")
            self.pc = ins.next_offset
        elif name == "LOAD":
            key = self.module.names[ins.operand]
            if key not in self.env:
                raise self._err(f"undefined variable {key!r}", ins)
            stack.append(self.env[key])
            self.pc = ins.next_offset
        elif name == "STORE":
            if len(stack) < 1:
                raise self._err("STORE with empty stack", ins)
            self.env[self.module.names[ins.operand]] = stack.pop()
            self.pc = ins.next_offset
        elif name == "POP":
            if len(stack) < 1:
                raise self._err("POP with empty stack", ins)
            stack.pop()
            self.pc = ins.next_offset
        elif name == "DUP":
            if len(stack) < 1:
                raise self._err("DUP with empty stack", ins)
            stack.append(stack[-1])
            self.pc = ins.next_offset
        elif name in ("NEG", "NOT"):
            if len(stack) < 1:
                raise self._err(f"{name} with empty stack", ins)
            value = stack.pop()
            if name == "NEG":
                if isinstance(value, bool) or not isinstance(value, int):
                    raise self._err("NEG requires an integer", ins)
                stack.append(-value)
            else:
                stack.append(not _truthy(value))
            self.pc = ins.next_offset
        elif name in ("JUMP", "JF", "JUMPT"):
            if name != "JUMP":
                if len(stack) < 1:
                    raise self._err(f"{name} with empty stack", ins)
                value = stack.pop()
                take = _truthy(value) if name == "JUMPT" else (not _truthy(value))
                self.pc = ins.operand if take else ins.next_offset
            else:
                self.pc = ins.operand
        else:
            if len(stack) < 2:
                raise self._err(f"{name} needs two operands", ins)
            right = stack.pop()
            left = stack.pop()
            stack.append(self._binary(name, left, right, self._span(ins)))
            self.pc = ins.next_offset

        self.steps += 1
        return name

    @staticmethod
    def _binary(name, left, right, span):
        try:
            if name == "ADD":
                return left + right
            if name == "SUB":
                return left - right
            if name == "MUL":
                return left * right
            if name == "DIV":
                if right == 0:
                    raise ZeroDivisionError
                return _trunc_div(left, right)
            if name == "MOD":
                if right == 0:
                    raise ZeroDivisionError
                return left - _trunc_div(left, right) * right
            if name == "EQ":
                return left == right
            if name == "NE":
                return left != right
            if name == "LT":
                return left < right
            if name == "GT":
                return left > right
            if name == "LE":
                return left <= right
            if name == "GE":
                return left >= right
        except ZeroDivisionError:
            raise RuntimeErr("division by zero", span)
        except TypeError:
            raise RuntimeErr(f"type error in {name}", span)
        raise RuntimeErr(f"unknown operator {name}", span)

    def run(self, budget=None):
        """Run until HALT. `budget` limits instructions executed by this call.
        Raises BudgetExceeded when the budget is hit; machine state
        (pc/stack/env/steps) is preserved and run() may be called again."""
        stop_at = self.steps + budget if budget is not None else None
        while not self.halted:
            if stop_at is not None and self.steps >= stop_at:
                raise BudgetExceeded(budget)
            self.step()
        return self.env
