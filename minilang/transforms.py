"""Local IR transforms (optimization passes).

Each pass takes an IRProgram and returns a *new* IRProgram; the input is never
mutated, so the pipeline can roll back simply by discarding the result.

Included passes:
  - ConstantFold          safe: folds CONST/CONST/op triples, remaps jumps
  - UnsafeZeroAddPeephole BUGGY: deletes `CONST 0, ADD` pairs but forgets to
                          remap jump targets (a classic broken pass, used to
                          demonstrate that the pipeline intercepts it)
  - ConstProp             safe: 1-for-1 LOAD -> CONST replacement, no relayout
  - AlgebraicPeephole     safe: removes x+0 / x-0 / x*1 with proper remapping
"""

from __future__ import annotations

from .interp import trunc_div, trunc_mod, Trap
from .ir import Instr, IRProgram, jump_targets, remove_instructions


def _fold_binop(op: str, a: int, b: int) -> int | None:
    """Fold a constant binop; None means 'do not fold' (would trap at runtime)."""
    try:
        if op == "ADD":
            return a + b
        if op == "SUB":
            return a - b
        if op == "MUL":
            return a * b
        if op == "DIV":
            return trunc_div(a, b)
        if op == "MOD":
            return trunc_mod(a, b)
        if op == "EQ":
            return int(a == b)
        if op == "NE":
            return int(a != b)
        if op == "LT":
            return int(a < b)
        if op == "LE":
            return int(a <= b)
        if op == "GT":
            return int(a > b)
        if op == "GE":
            return int(a >= b)
    except Trap:
        return None
    return None


FOLDABLE = {"ADD", "SUB", "MUL", "DIV", "MOD", "EQ", "NE", "LT", "LE", "GT", "GE"}


class ConstantFold:
    name = "constant-fold"

    def apply(self, prog: IRProgram) -> IRProgram:
        instrs = prog.instrs
        targets = jump_targets(instrs)
        n = len(instrs)
        new: list[Instr] = []
        old_to_new: dict[int, int] = {}
        changed = False
        i = 0
        while i < n:
            if (
                i + 2 < n
                and instrs[i].op == "CONST"
                and instrs[i + 1].op == "CONST"
                and instrs[i + 2].op in FOLDABLE
                and (i + 1) not in targets
                and (i + 2) not in targets
            ):
                value = _fold_binop(instrs[i + 2].op, instrs[i].arg, instrs[i + 1].arg)
                if value is not None:
                    old_to_new[i] = len(new)
                    new.append(Instr("CONST", value))
                    i += 3
                    changed = True
                    continue
            old_to_new[i] = len(new)
            new.append(instrs[i])
            i += 1
        if not changed:
            return prog
        remapped = [
            Instr(ins.op, old_to_new[ins.arg]) if ins.op in {"JMP", "JZ"} else ins
            for ins in new
        ]
        return IRProgram(remapped, prog.inputs)


class UnsafeZeroAddPeephole:
    """Deliberately broken: removes `CONST 0, ADD` pairs WITHOUT remapping
    jump targets. Any absolute JMP/JZ index past a deleted pair goes stale,
    which the structural validator (or the differential checker) must catch."""

    name = "zero-add-peephole (UNSAFE: no jump fixup)"

    def apply(self, prog: IRProgram) -> IRProgram:
        instrs = prog.instrs
        new: list[Instr] = []
        i = 0
        while i < len(instrs):
            if (
                i + 1 < len(instrs)
                and instrs[i].op == "CONST"
                and instrs[i].arg == 0
                and instrs[i + 1].op == "ADD"
            ):
                i += 2  # BUG: instructions dropped, jump args left stale
                continue
            new.append(instrs[i])
            i += 1
        return IRProgram(new, prog.inputs)


class ConstProp:
    """Linear constant propagation. Replaces LOAD x with CONST k when x is
    provably k on every path reaching the load. One-for-one replacement, so
    instruction indices (and therefore jump targets) never move."""

    name = "const-prop"

    def apply(self, prog: IRProgram) -> IRProgram:
        instrs = prog.instrs
        targets = jump_targets(instrs)
        env: dict[str, int] = {}
        new: list[Instr] = []
        for idx, ins in enumerate(instrs):
            is_target = idx in targets
            if is_target:
                env = {}
            if ins.op == "LOAD" and ins.arg in env:
                new.append(Instr("CONST", env[ins.arg]))
                continue
            if ins.op == "STORE":
                prev = instrs[idx - 1] if idx > 0 else None
                if (
                    not is_target
                    and prev is not None
                    and prev.op == "CONST"
                    and (idx - 1) not in targets
                ):
                    env[ins.arg] = prev.arg
                else:
                    env.pop(ins.arg, None)
            elif ins.op in ("JMP", "JZ"):
                env = {}
            new.append(ins)
        return IRProgram(new, prog.inputs)


class AlgebraicPeephole:
    """Removes `CONST 0, ADD` / `CONST 0, SUB` / `CONST 1, MUL` pairs
    (i.e. x+0, x-0, x*1) with correct jump-target remapping."""

    name = "algebraic-peephole"

    IDENTITY = {"ADD": 0, "SUB": 0, "MUL": 1}

    def apply(self, prog: IRProgram) -> IRProgram:
        instrs = prog.instrs
        targets = jump_targets(instrs)
        removed: set[int] = set()
        i = 0
        while i + 1 < len(instrs):
            a, b = instrs[i], instrs[i + 1]
            if (
                a.op == "CONST"
                and b.op in self.IDENTITY
                and a.arg == self.IDENTITY[b.op]
                and i not in targets
                and (i + 1) not in targets
            ):
                removed.add(i)
                removed.add(i + 1)
                i += 2
            else:
                i += 1
        if not removed:
            return prog
        return IRProgram(remove_instructions(instrs, removed), prog.inputs)
