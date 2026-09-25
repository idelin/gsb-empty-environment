"""栈式字节码解释器（自行实现，不使用 eval 或任何现成编译器）。"""
from __future__ import annotations


class RuntimeFault(Exception):
    def __init__(self, pc: int, message: str):
        super().__init__(message)
        self.pc = pc
        self.message = message


def execute(instrs, output):
    env = {}
    stack = []
    pc = 0
    while True:
        ins = instrs[pc]
        op = ins.op
        if op == "PUSH":
            stack.append(ins.arg)
        elif op == "LOAD":
            if ins.arg not in env:
                raise RuntimeFault(pc, f"未定义变量: {ins.arg}")
            stack.append(env[ins.arg])
        elif op == "STORE":
            env[ins.arg] = stack.pop()
        elif op in ("ADD", "SUB", "MUL", "DIV"):
            b = stack.pop()
            a = stack.pop()
            if op == "ADD":
                stack.append(a + b)
            elif op == "SUB":
                stack.append(a - b)
            elif op == "MUL":
                stack.append(a * b)
            else:
                if b == 0:
                    raise RuntimeFault(pc, "division by zero")
                stack.append(a // b)
        elif op == "NEG":
            stack.append(-stack.pop())
        elif op == "PRINT":
            output.append(stack.pop())
        elif op == "HALT":
            return env
        else:
            raise RuntimeFault(pc, f"未知指令: {op}")
        pc += 1
