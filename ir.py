"""核心 IR：指令定义、文本语法解析器（源码位置记录为 UTF-8 字节区间）、结构验证器。

语法（行式，# 之后为注释）：
    标签:          loop:
    const x, 5     mov x, y      add x, y, z   sub x, y, z   mul x, y, z
    input x, 0     print x
    jmp loop       brz x, loop   brnz x, loop
    halt           nop
"""
from __future__ import annotations

import re
from dataclasses import dataclass

# 指令 -> 操作数种类序列。reg=寄存器, int=整数字面量, label=分支目标, labeldef=标签定义
OPS = {
    "label": ("labeldef",),
    "const": ("reg", "int"),
    "mov": ("reg", "reg"),
    "add": ("reg", "reg", "reg"),
    "sub": ("reg", "reg", "reg"),
    "mul": ("reg", "reg", "reg"),
    "input": ("reg", "int"),
    "print": ("reg",),
    "jmp": ("label",),
    "brz": ("reg", "label"),
    "brnz": ("reg", "label"),
    "halt": (),
    "nop": (),
}

NAME_RE = re.compile(r"^[a-z][a-z0-9_]*$")


@dataclass(frozen=True)
class Span:
    """源码位置：UTF-8 字节区间 [start, end)。"""
    start: int
    end: int


@dataclass
class Instr:
    op: str
    args: tuple
    span: Span | None = None

    def text(self) -> str:
        if self.op == "label":
            return f"{self.args[0]}:"
        if self.args:
            return f"{self.op} " + ", ".join(self.args)
        return self.op


def parse(source: str) -> list[Instr]:
    """把源码文本解析为指令序列，每条指令携带 UTF-8 字节区间。"""
    instrs: list[Instr] = []
    byte_offset = 0
    for line in source.splitlines(keepends=True):
        line_start = byte_offset
        byte_offset += len(line.encode("utf-8"))
        line_text = line.rstrip("\r\n")
        code = line_text.split("#", 1)[0].strip()
        if not code:
            continue
        # 定位 code 在行内的字节区间（前导空白按 UTF-8 精确换算）
        lead_chars = len(line_text) - len(line_text.lstrip())
        start = line_start + len(line_text[:lead_chars].encode("utf-8"))
        span = Span(start, start + len(code.encode("utf-8")))
        instrs.append(_parse_instr(code, span))
    return instrs


def _parse_instr(code: str, span: Span) -> Instr:
    if code.endswith(":"):
        return Instr("label", (code[:-1].strip(),), span)
    head, _, rest = code.partition(" ")
    args = tuple(a.strip() for a in rest.split(",")) if rest else ()
    return Instr(head, args, span)


def validate(prog: list[Instr]) -> list[str]:
    """结构验证：返回错误列表（空列表表示通过）。不做语义/全域正确性判断。"""
    errors: list[str] = []
    labels: dict[str, int] = {}
    for i, ins in enumerate(prog):
        if ins.op not in OPS:
            errors.append(f"指令 {i}: 未知操作 '{ins.op}'")
            continue
        kinds = OPS[ins.op]
        if len(ins.args) != len(kinds):
            errors.append(
                f"指令 {i} ({ins.op}): 操作数个数 {len(ins.args)}，应为 {len(kinds)}")
            continue
        if ins.op == "label":
            name = ins.args[0]
            if name in labels:
                errors.append(f"指令 {i}: 重复标签 '{name}'（首次定义于指令 {labels[name]}）")
            labels[name] = i
        for arg, kind in zip(ins.args, kinds):
            if kind in ("reg", "labeldef") and not NAME_RE.match(arg):
                errors.append(f"指令 {i} ({ins.op}): 非法名字 '{arg}'")
            elif kind == "int":
                try:
                    int(arg)
                except ValueError:
                    errors.append(f"指令 {i} ({ins.op}): '{arg}' 不是整数字面量")
    # 分支目标存在性：收集完所有标签后统一检查
    for i, ins in enumerate(prog):
        if ins.op not in OPS:
            continue
        for arg, kind in zip(ins.args, OPS[ins.op]):
            if kind == "label" and arg not in labels:
                errors.append(f"指令 {i} ({ins.op}): 分支目标 '{arg}' 不存在")
    return errors


def format_program(prog: list[Instr], show_spans: bool = False) -> str:
    lines = []
    for ins in prog:
        text = ins.text()
        if show_spans and ins.span is not None:
            text = f"{text:<24} ; bytes[{ins.span.start},{ins.span.end})"
        lines.append(text)
    return "\n".join(lines)
