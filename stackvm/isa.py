"""栈机指令集: 定义、字节码编解码与反汇编。

指令按字节序列化, 跳转目标是字节偏移。多字节指令的"内部"位置
不是有效指令边界, 验证器据此拒绝跳到指令中间的非法跳转。
"""
from __future__ import annotations

from dataclasses import dataclass


class DecodeError(Exception):
    pass


@dataclass(frozen=True)
class OpInfo:
    name: str
    code: int
    operand_size: int = 0
    signed: bool = False


OPS = [
    OpInfo("HALT", 0x00),
    OpInfo("PUSH", 0x01, 4, signed=True),
    OpInfo("LOAD", 0x02, 1),
    OpInfo("STORE", 0x03, 1),
    OpInfo("ADD", 0x04),
    OpInfo("SUB", 0x05),
    OpInfo("MUL", 0x06),
    OpInfo("DIV", 0x07),
    OpInfo("NEG", 0x08),
    OpInfo("EQ", 0x09),
    OpInfo("NE", 0x0A),
    OpInfo("LT", 0x0B),
    OpInfo("LE", 0x0C),
    OpInfo("GT", 0x0D),
    OpInfo("GE", 0x0E),
    OpInfo("NOT", 0x0F),
    OpInfo("JMP", 0x10, 2),
    OpInfo("JZ", 0x11, 2),
    OpInfo("POP", 0x12),
    OpInfo("PRINT", 0x13),
    OpInfo("DUP", 0x14),
]

BY_NAME = {op.name: op for op in OPS}
BY_CODE = {op.code: op for op in OPS}

# 每条指令的栈效应: (弹出个数, 压入个数)
STACK_EFFECT = {
    "HALT": (0, 0),
    "PUSH": (0, 1),
    "LOAD": (0, 1),
    "STORE": (1, 0),
    "ADD": (2, 1),
    "SUB": (2, 1),
    "MUL": (2, 1),
    "DIV": (2, 1),
    "NEG": (1, 1),
    "EQ": (2, 1),
    "NE": (2, 1),
    "LT": (2, 1),
    "LE": (2, 1),
    "GT": (2, 1),
    "GE": (2, 1),
    "NOT": (1, 1),
    "JMP": (0, 0),
    "JZ": (1, 0),
    "POP": (1, 0),
    "PRINT": (1, 0),
    "DUP": (1, 2),
}


def encode(name: str, operand: int | None = None) -> bytes:
    info = BY_NAME[name]
    out = bytearray([info.code])
    if info.operand_size:
        if operand is None:
            raise ValueError(f"{name} 需要操作数")
        out += int(operand).to_bytes(
            info.operand_size, "big", signed=info.signed
        )
    return bytes(out)


def decode_at(
    code: bytes, pc: int
) -> tuple[OpInfo, int | None, int]:
    if pc < 0 or pc >= len(code):
        raise DecodeError(f"pc={pc} 超出代码范围")
    try:
        info = BY_CODE[code[pc]]
    except KeyError:
        raise DecodeError(f"pc={pc}: 未知操作码 0x{code[pc]:02x}") from None
    end = pc + 1 + info.operand_size
    if end > len(code):
        raise DecodeError(f"pc={pc}: {info.name} 的操作数被截断")
    operand: int | None = None
    if info.operand_size:
        operand = int.from_bytes(
            code[pc + 1 : end], "big", signed=info.signed
        )
    return info, operand, end


def decode_all(
    code: bytes,
) -> dict[int, tuple[OpInfo, int | None, int]]:
    """顺序解码整个字节码, 返回 {指令边界 pc: (信息, 操作数, 下一pc)}。"""
    boundaries: dict[int, tuple[OpInfo, int | None, int]] = {}
    pc = 0
    while pc < len(code):
        info, operand, nxt = decode_at(code, pc)
        boundaries[pc] = (info, operand, nxt)
        pc = nxt
    return boundaries


def disassemble(code: bytes) -> str:
    lines = []
    for pc, (info, operand, _nxt) in decode_all(code).items():
        if operand is None:
            lines.append(f"{pc:04d}  {info.name}")
        else:
            lines.append(f"{pc:04d}  {info.name:<6} {operand}")
    return "\n".join(lines)
