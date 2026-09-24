"""局部变换集。每个变换是纯函数：list[Instr] -> list[Instr]。

其中 buggy_label_cleanup 是故意有缺陷的变换：它只扫描 jmp 引用就删除
“未使用”的标签，会破坏 brz/brnz 的分支目标，用于演示结构验证拦截与回滚。
"""
from __future__ import annotations

from ir import Instr

_ARITH = {"add": lambda a, b: a + b, "sub": lambda a, b: a - b, "mul": lambda a, b: a * b}
_WRITE_OPS = {"const", "mov", "add", "sub", "mul", "input"}


def fold_constants(prog: list[Instr]) -> list[Instr]:
    """基本块内常量折叠：add/sub/mul 的两个操作数均为已知常量时替换为 const。"""
    out: list[Instr] = []
    env: dict[str, int] = {}
    for ins in prog:
        if ins.op == "label":
            env.clear()
            out.append(ins)
            continue
        if ins.op in _ARITH and ins.args[1] in env and ins.args[2] in env:
            value = _ARITH[ins.op](env[ins.args[1]], env[ins.args[2]])
            out.append(Instr("const", (ins.args[0], str(value)), ins.span))
            env[ins.args[0]] = value
            continue
        out.append(ins)
        if ins.op == "const":
            env[ins.args[0]] = int(ins.args[1])
        elif ins.op in _WRITE_OPS:
            env.pop(ins.args[0], None)
        if ins.op in ("jmp", "halt"):
            env.clear()
    return out


def remove_nops(prog: list[Instr]) -> list[Instr]:
    """删除 nop 空指令。"""
    return [ins for ins in prog if ins.op != "nop"]


def eliminate_dead_writes(prog: list[Instr]) -> list[Instr]:
    """删除目标寄存器在全程序内从未被读取的 const/mov/add/sub/mul。"""
    read: set[str] = set()
    for ins in prog:
        if ins.op == "mov":
            read.add(ins.args[1])
        elif ins.op in _ARITH:
            read.update((ins.args[1], ins.args[2]))
        elif ins.op in ("print", "brz", "brnz"):
            read.add(ins.args[0])
    removable = {"const", "mov", "add", "sub", "mul"}
    return [ins for ins in prog
            if not (ins.op in removable and ins.args[0] not in read)]


def remove_unreachable(prog: list[Instr]) -> list[Instr]:
    """删除 halt/jmp 之后、下一个标签之前的不可达指令。"""
    out: list[Instr] = []
    skipping = False
    for ins in prog:
        if ins.op == "label":
            skipping = False
            out.append(ins)
            continue
        if skipping:
            continue
        out.append(ins)
        if ins.op in ("halt", "jmp"):
            skipping = True
    return out


def buggy_label_cleanup(prog: list[Instr]) -> list[Instr]:
    """【有缺陷】只检查 jmp 引用就删除“未使用”标签，会遗漏 brz/brnz 的引用，
    从而产生悬空分支目标。流水线必须拦截并回滚该步。"""
    referenced = {ins.args[0] for ins in prog if ins.op == "jmp"}
    return [ins for ins in prog
            if not (ins.op == "label" and ins.args[0] not in referenced)]
