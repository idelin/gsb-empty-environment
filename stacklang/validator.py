"""Static validation of bytecode.

Checks:
1. Decodes cleanly (known opcodes, complete operands).
2. All jump targets are inside the code and land on instruction boundaries,
   never in the middle of another instruction.
3. PUSH_CONST / LOAD / STORE operand indices are in range.
4. Along every control-flow edge the stack depth is consistent; no underflow;
   no branch joins where the depths disagree; every terminating edge has
   depth 0.
"""

from .bytecode import OPCODES, JUMP_OPS, OP_BY_NAME, BytecodeModule, decode, DecodeError
from .errors import ValidationError

FALLTHROUGH_TERMINATORS = frozenset({
    OP_BY_NAME["HALT"], OP_BY_NAME["JUMP"],
})

STACK_DELTA = {
    "HALT": 0, "PUSH_CONST": +1, "PUSH_TRUE": +1, "PUSH_FALSE": +1,
    "LOAD": +1, "STORE": -1, "POP": -1, "DUP": +1,
    "ADD": -1, "SUB": -1, "MUL": -1, "DIV": -1, "MOD": -1,
    "NEG": 0, "NOT": 0,
    "EQ": -1, "NE": -1, "LT": -1, "GT": -1, "LE": -1, "GE": -1,
    "JF": -1, "JUMPT": -1, "JUMP": 0,
}


def validate(module):
    code = module.code
    if not code:
        raise ValidationError("empty code section")

    try:
        instructions, boundaries = decode(module)
    except DecodeError as exc:
        raise ValidationError(f"{exc.message} at byte {exc.offset}")

    by_offset = {ins.offset: ins for ins in instructions}
    total = len(code)

    for ins in instructions:
        name = ins.name
        if name == "PUSH_CONST" and ins.operand >= len(module.consts):
            raise ValidationError(
                f"PUSH_CONST index {ins.operand} out of range at byte {ins.offset}")
        if name == "LOAD" and ins.operand >= len(module.names):
            raise ValidationError(
                f"LOAD name index {ins.operand} out of range at byte {ins.offset}")
        if name == "STORE" and ins.operand >= len(module.names):
            raise ValidationError(
                f"STORE name index {ins.operand} out of range at byte {ins.offset}")
        if ins.op in JUMP_OPS:
            target = ins.operand
            if target >= total:
                raise ValidationError(
                    f"jump at byte {ins.offset} targets byte {target} past end {total}")
            if target not in boundaries:
                owner = max(b for b in boundaries if b < target)
                raise ValidationError(
                    f"jump at byte {ins.offset} lands at byte {target}, "
                    f"in the middle of an instruction starting at byte {owner}")

    # CFG stack-height analysis (worklist over instruction boundaries).
    depth = {0: 0}
    worklist = [0]
    while worklist:
        offset = worklist.pop()
        current = depth[offset]
        ins = by_offset[offset]
        name = ins.name
        delta = STACK_DELTA[name]

        if current + delta < 0:
            raise ValidationError(
                f"stack underflow at byte {offset} ({name}); depth was {current}")
        after = current + delta

        def visit(target, target_depth):
            if target == total:
                if target_depth != 0:
                    raise ValidationError(
                        f"end of code reached with stack depth {target_depth} "
                        f"(from byte {offset}); expected 0")
                return
            old = depth.get(target)
            if old is None:
                depth[target] = target_depth
                worklist.append(target)
            elif old != target_depth:
                raise ValidationError(
                    f"stack depth mismatch at byte {target}: reached with "
                    f"depth {old} and {target_depth}")

        if name == "HALT":
            visit(total, after)
            continue

        if name == "JUMP":
            visit(ins.operand, after)
            continue

        if name in ("JF", "JUMPT"):
            visit(ins.operand, after)
            visit(ins.next_offset, after)
            continue

        visit(ins.next_offset, after)

    if 0 not in depth:
        raise ValidationError("code has no reachable entry")
    return True
