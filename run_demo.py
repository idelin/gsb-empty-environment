#!/usr/bin/env python3
"""Run the stacklang examples: compile, validate, execute with a budget."""

import pathlib

from stacklang import (
    compile_source, validate, VM, disassemble, ValidationError,
    BudgetExceeded, BytecodeModule,
)

ROOT = pathlib.Path(__file__).parent


def show_file(name):
    source = (ROOT / name).read_text(encoding="utf-8")
    print("=" * 70)
    print("SOURCE:", name)
    print("-" * 70)
    print(source.rstrip())
    module = compile_source(source)
    print("-" * 70)
    validate(module)
    print("BYTECODE (validator accepted):")
    print(disassemble(module))
    vm = VM(module)
    vm.run(budget=100_000)
    print("-" * 70)
    print("VARIABLES:", {k: v for k, v in vm.env.items()})
    print("INSTRUCTIONS EXECUTED:", vm.steps)
    print()


def show_stepping():
    print("=" * 70)
    print("SINGLE STEPPING")
    print("-" * 70)
    source = "let x = 2; x = x * x + 1;"
    print(source)
    vm = VM(compile_source(source))
    n = 0
    while not vm.halted:
        op = vm.step()
        n += 1
        print(f"  step {n:2d}: {op:10s} stack={vm.stack} env={vm.env}")
    print("final:", vm.env)
    print()


def show_budget():
    print("=" * 70)
    print("BUDGET (infinite loop, 12 instructions per slice)")
    print("-" * 70)
    vm = VM(compile_source("let n = 0; while (true) { n = n + 1; }"))
    for slice_no in range(1, 4):
        try:
            vm.run(budget=12)
        except BudgetExceeded:
            print(f"  slice {slice_no}: paused at steps={vm.steps}, n={vm.env['n']}")
    print()


def show_tampering():
    print("=" * 70)
    print("TAMPERED JUMP -> VALIDATOR REJECTION")
    print("-" * 70)
    module = compile_source("let x = 1; if (x == 1) { x = 2; } else { x = 3; } x")
    # Point the first conditional jump at the operand byte of a 3-byte
    # PUSH_CONST instruction: decoding still works, target is not a boundary.
    push_const = module.code.index(0x02)
    jf = module.code.index(0x15)
    corrupted = bytearray(module.code)
    corrupted[jf + 1:jf + 5] = (push_const + 1).to_bytes(4, "big")
    broken = BytecodeModule(corrupted, module.consts, module.names, module.spans)
    try:
        validate(broken)
        print("  ERROR: validator accepted corrupted bytecode")
    except ValidationError as exc:
        print("  rejected:", exc)
    try:
        VM(broken)
        print("  ERROR: VM accepted corrupted bytecode")
    except ValidationError as exc:
        print("  VM also refuses to run it:", exc)
    print()


if __name__ == "__main__":
    show_file("examples/nested_break.sl")
    show_file("examples/short_circuit.sl")
    show_stepping()
    show_budget()
    show_tampering()
