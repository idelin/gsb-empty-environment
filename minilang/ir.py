"""Stack-machine IR and structural validation.

Instructions are a flat list; JMP/JZ carry *absolute* instruction indices as
targets. Any transform that deletes or inserts instructions must therefore
remap jump targets -- forgetting to do so is exactly the class of bug the
structural validator is designed to catch.
"""

from __future__ import annotations

from dataclasses import dataclass

OPS_NO_ARG = {
    "ADD", "SUB", "MUL", "DIV", "MOD", "NEG",
    "EQ", "NE", "LT", "LE", "GT", "GE",
    "OUTPUT", "HALT",
}
OPS_INT_ARG = {"CONST", "INPUT", "JMP", "JZ"}
OPS_NAME_ARG = {"LOAD", "STORE"}
JUMP_OPS = {"JMP", "JZ"}
ALL_OPS = OPS_NO_ARG | OPS_INT_ARG | OPS_NAME_ARG


@dataclass(frozen=True)
class Instr:
    op: str
    arg: object = None  # int for CONST/INPUT/JMP/JZ, str for LOAD/STORE


@dataclass
class IRProgram:
    instrs: list[Instr]
    inputs: int  # number of input slots the program reads

    def __post_init__(self):
        self.instrs = list(self.instrs)


def jump_targets(instrs: list[Instr]) -> set[int]:
    return {ins.arg for ins in instrs if ins.op in JUMP_OPS and isinstance(ins.arg, int)}


def validate(prog: IRProgram) -> list[str]:
    """Structural checks. Returns a list of problems; empty means well-formed."""
    errors: list[str] = []
    instrs = prog.instrs
    n = len(instrs)
    if n == 0:
        errors.append("program is empty")
        return errors
    for idx, ins in enumerate(instrs):
        if ins.op not in ALL_OPS:
            errors.append(f"[{idx}] unknown opcode {ins.op!r}")
            continue
        if ins.op in JUMP_OPS:
            if not isinstance(ins.arg, int):
                errors.append(f"[{idx}] {ins.op} target is not an integer: {ins.arg!r}")
            elif not 0 <= ins.arg < n:
                errors.append(
                    f"[{idx}] {ins.op} target {ins.arg} out of range [0, {n})"
                )
        elif ins.op in OPS_INT_ARG:
            if not isinstance(ins.arg, int):
                errors.append(f"[{idx}] {ins.op} expects an int arg, got {ins.arg!r}")
            elif ins.op == "INPUT" and ins.arg < 0:
                errors.append(f"[{idx}] INPUT index must be >= 0, got {ins.arg}")
        elif ins.op in OPS_NAME_ARG:
            if not isinstance(ins.arg, str):
                errors.append(f"[{idx}] {ins.op} expects a name arg, got {ins.arg!r}")
        elif ins.arg is not None:
            errors.append(f"[{idx}] {ins.op} takes no arg, got {ins.arg!r}")
    if not any(ins.op == "HALT" for ins in instrs):
        errors.append("program has no HALT instruction")
    if instrs[-1].op not in ("HALT", "JMP"):
        errors.append(
            f"last instruction is {instrs[-1].op}, control may fall off the end"
        )
    return errors


def remove_instructions(instrs: list[Instr], removed: set[int]) -> list[Instr]:
    """Remove instructions and remap jump targets. Refuses to drop a jump target."""
    dangling = jump_targets(instrs) & removed
    if dangling:
        raise ValueError(f"cannot remove jump target instruction(s): {sorted(dangling)}")
    old_to_new: dict[int, int] = {}
    new: list[Instr] = []
    for i, ins in enumerate(instrs):
        if i in removed:
            continue
        old_to_new[i] = len(new)
        new.append(ins)
    return [
        Instr(ins.op, old_to_new[ins.arg]) if ins.op in JUMP_OPS else ins
        for ins in new
    ]


def format_ir(prog: IRProgram) -> str:
    lines = []
    for i, ins in enumerate(prog.instrs):
        arg = "" if ins.arg is None else f" {ins.arg}"
        lines.append(f"{i:04d}: {ins.op}{arg}")
    return "\n".join(lines)
