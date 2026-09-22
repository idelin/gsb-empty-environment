"""Bytecode format.

Encoding (variable length):
  no-operand opcode                      1 byte
  PUSH_CONST / LOAD / STORE              opcode + uint16 (big endian)
  JF / JUMP                             opcode + uint32 target byte offset

Instruction boundaries are byte offsets at which an instruction starts.
A jump target is valid only if it equals an instruction boundary and is
inside the code section (0 is valid, len(code) is not).
"""

# opcode: (name, operand arity in bytes)
OPCODES = {
    0x01: ("HALT", 0),
    0x02: ("PUSH_CONST", 2),
    0x03: ("PUSH_TRUE", 0),
    0x04: ("PUSH_FALSE", 0),
    0x05: ("LOAD", 2),
    0x06: ("STORE", 2),
    0x07: ("POP", 0),
    0x08: ("ADD", 0),
    0x09: ("SUB", 0),
    0x0A: ("MUL", 0),
    0x0B: ("DIV", 0),
    0x0C: ("MOD", 0),
    0x0D: ("NEG", 0),
    0x0E: ("NOT", 0),
    0x0F: ("EQ", 0),
    0x10: ("NE", 0),
    0x11: ("LT", 0),
    0x12: ("GT", 0),
    0x13: ("LE", 0),
    0x14: ("GE", 0),
    0x15: ("JF", 4),
    0x16: ("JUMP", 4),
    0x17: ("JUMPT", 4),
    0x18: ("DUP", 0),
}

OP_BY_NAME = {name: code for code, (name, _) in OPCODES.items()}

JUMP_OPS = frozenset({OP_BY_NAME["JF"], OP_BY_NAME["JUMP"], OP_BY_NAME["JUMPT"]})


class Label:
    __slots__ = ("at",)

    def __init__(self):
        self.at = None


class Instr:
    __slots__ = ("op", "operand", "span")

    def __init__(self, op, operand=None, span=None):
        self.op = op
        self.operand = operand
        self.span = span

    @property
    def name(self):
        return OPCODES[self.op][0]

    @property
    def arity(self):
        return OPCODES[self.op][1]


class BytecodeModule:
    def __init__(self, code, consts=(), names=(), spans=None):
        self.code = bytes(code)
        self.consts = tuple(consts)
        self.names = tuple(names)
        self.spans = dict(spans or {})


def instr_size(instr):
    return 1 + instr.arity


def assemble(instructions, consts=(), names=(), spans=None):
    """Assemble instructions with Label operands into a BytecodeModule."""
    sizes = [instr_size(ins) for ins in instructions]

    # Place labels using the offsets that precede each instruction.
    positions = []
    cursor = 0
    for ins, size in zip(instructions, sizes):
        positions.append(cursor)
        cursor += size

    for ins in instructions:
        if isinstance(ins.operand, Label) and ins.operand.at is None:
            raise ValueError("unplaced label")

    code = bytearray()
    resolved_spans = dict(spans or {})
    for pos, ins in zip(positions, instructions):
        code.append(ins.op)
        operand = ins.operand
        if isinstance(operand, Label):
            operand = operand.at
        if ins.arity == 2:
            if not 0 <= operand <= 0xFFFF:
                raise ValueError("uint16 operand out of range")
            code += operand.to_bytes(2, "big")
        elif ins.arity == 4:
            if not 0 <= operand <= 0xFFFFFFFF:
                raise ValueError("uint32 operand out of range")
            code += operand.to_bytes(4, "big")
        resolved_spans[pos] = ins.span

    return BytecodeModule(code, consts, names, resolved_spans)


class DecodedInstr:
    __slots__ = ("offset", "op", "operand")

    def __init__(self, offset, op, operand):
        self.offset = offset
        self.op = op
        self.operand = operand

    @property
    def name(self):
        return OPCODES[self.op][0]

    @property
    def next_offset(self):
        return self.offset + 1 + OPCODES[self.op][1]


class DecodeError(Exception):
    def __init__(self, message, offset):
        super().__init__(message)
        self.message = message
        self.offset = offset


def decode(module_or_code):
    """Decode code bytes. Raises DecodeError pointing at the bad byte."""
    if isinstance(module_or_code, BytecodeModule):
        code = module_or_code.code
    else:
        code = bytes(module_or_code)
    instructions = []
    boundaries = set()
    pos = 0
    total = len(code)
    while pos < total:
        op = code[pos]
        if op not in OPCODES:
            raise DecodeError(f"unknown opcode 0x{op:02X}", pos)
        arity = OPCODES[op][1]
        if pos + 1 + arity > total:
            raise DecodeError("truncated instruction operand", pos)
        boundaries.add(pos)
        if arity == 0:
            operand = None
        elif arity == 2:
            operand = int.from_bytes(code[pos + 1:pos + 3], "big")
        else:
            operand = int.from_bytes(code[pos + 1:pos + 5], "big")
        instructions.append(DecodedInstr(pos, op, operand))
        pos += 1 + arity
    return instructions, boundaries
