"""IR 解释执行器（自行实现，不依赖 eval 或现成编译器）。"""
from __future__ import annotations

from ir import Instr


class StepLimitExceeded(RuntimeError):
    pass


def run(prog: list[Instr], inputs: tuple = (), step_limit: int = 100_000):
    """执行程序，返回 (输出列表, 最终寄存器表)。inputs 为 input 指令的数据源。"""
    labels = {ins.args[0]: i for i, ins in enumerate(prog) if ins.op == "label"}
    regs: dict[str, int] = {}
    out: list[int] = []
    pc = 0
    steps = 0

    def val(name: str) -> int:
        if name not in regs:
            raise RuntimeError(f"读取未定义寄存器 '{name}'")
        return regs[name]

    while pc < len(prog):
        steps += 1
        if steps > step_limit:
            raise StepLimitExceeded(f"超过步数上限 {step_limit}（疑似死循环）")
        ins = prog[pc]
        op, args = ins.op, ins.args
        if op == "const":
            regs[args[0]] = int(args[1])
        elif op == "mov":
            regs[args[0]] = val(args[1])
        elif op in ("add", "sub", "mul"):
            a, b = val(args[1]), val(args[2])
            regs[args[0]] = {"add": a + b, "sub": a - b, "mul": a * b}[op]
        elif op == "input":
            k = int(args[1])
            regs[args[0]] = inputs[k] if k < len(inputs) else 0
        elif op == "print":
            out.append(val(args[0]))
        elif op == "jmp":
            pc = labels[args[0]]
            continue
        elif op == "brz":
            if val(args[0]) == 0:
                pc = labels[args[1]]
                continue
        elif op == "brnz":
            if val(args[0]) != 0:
                pc = labels[args[1]]
                continue
        elif op == "halt":
            break
        elif op in ("label", "nop"):
            pass
        else:
            raise RuntimeError(f"无法执行未知指令 '{op}'")
        pc += 1
    return out, regs
