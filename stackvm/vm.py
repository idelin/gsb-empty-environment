"""栈机解释器: 支持单步执行 (step) 与指令条数预算 (budget)。"""
from __future__ import annotations

from . import isa


class VMError(Exception):
    pass


class BudgetExhausted(VMError):
    pass


class Machine:
    def __init__(
        self,
        code: bytes,
        budget: int | None = None,
        trace: bool = False,
    ):
        self.code = code
        try:
            self.boundaries = isa.decode_all(code)
        except isa.DecodeError as exc:
            raise VMError(f"字节码解码失败: {exc}") from None
        self.budget = budget
        self.pc = 0
        self.stack: list[int] = []
        self.slots: list[int] = [0] * 256
        self.output: list[int] = []
        self.steps = 0
        self.halted = False
        self.trace_enabled = trace
        self.trace: list[int] = []  # 已执行指令的 pc 序列

    def _pop(self) -> int:
        if not self.stack:
            raise VMError(f"pc={self.pc}: 栈下溢")
        return self.stack.pop()

    def _binop(self, fn):
        right = self._pop()
        left = self._pop()
        self.stack.append(fn(left, right))

    def _cmpop(self, fn):
        right = self._pop()
        left = self._pop()
        self.stack.append(1 if fn(left, right) else 0)

    def step(self) -> bool:
        """执行一条指令。返回 False 表示机器已停机。"""
        if self.halted:
            return False
        if self.budget is not None and self.steps >= self.budget:
            raise BudgetExhausted(f"指令预算 {self.budget} 已耗尽")
        if self.pc not in self.boundaries:
            raise VMError(f"pc={self.pc}: 不在指令边界上")

        info, operand, _nxt = isa.decode_at(self.code, self.pc)
        if self.trace_enabled:
            self.trace.append(self.pc)
        self.steps += 1
        name = info.name

        if name == "HALT":
            self.halted = True
            return False

        # 顺序指令默认推进 pc, 跳转指令再覆盖
        self.pc += 1 + info.operand_size

        if name == "PUSH":
            self.stack.append(operand)
        elif name == "LOAD":
            self.stack.append(self.slots[operand])
        elif name == "STORE":
            self.slots[operand] = self._pop()
        elif name == "ADD":
            self._binop(lambda a, b: a + b)
        elif name == "SUB":
            self._binop(lambda a, b: a - b)
        elif name == "MUL":
            self._binop(lambda a, b: a * b)
        elif name == "DIV":
            right = self._pop()
            left = self._pop()
            if right == 0:
                raise VMError("除零错误")
            quotient = abs(left) // abs(right)
            if (left < 0) != (right < 0):
                quotient = -quotient
            self.stack.append(quotient)
        elif name == "NEG":
            self.stack.append(-self._pop())
        elif name == "EQ":
            self._cmpop(lambda a, b: a == b)
        elif name == "NE":
            self._cmpop(lambda a, b: a != b)
        elif name == "LT":
            self._cmpop(lambda a, b: a < b)
        elif name == "LE":
            self._cmpop(lambda a, b: a <= b)
        elif name == "GT":
            self._cmpop(lambda a, b: a > b)
        elif name == "GE":
            self._cmpop(lambda a, b: a >= b)
        elif name == "NOT":
            self.stack.append(0 if self._pop() else 1)
        elif name == "JMP":
            self.pc = operand
        elif name == "JZ":
            if self._pop() == 0:
                self.pc = operand
        elif name == "POP":
            self._pop()
        elif name == "PRINT":
            self.output.append(self._pop())
        elif name == "DUP":
            if not self.stack:
                raise VMError("DUP 时栈为空")
            self.stack.append(self.stack[-1])
        else:  # 解码层已保证不会发生
            raise VMError(f"未知指令 {name}")
        return True

    def run(self) -> list[int]:
        while self.step():
            pass
        return self.output
