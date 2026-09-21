"""端到端运行样例: 编译、验证、单步、预算、损坏跳转。

运行: python3.11 demo.py
"""
from __future__ import annotations

from stackvm import (
    BudgetExhausted,
    Machine,
    VerifyError,
    compile_program,
    isa,
    parse,
    verify,
)


SAMPLE = """# 嵌套循环 + break + 短路 + if/else
let total = 0;
let i = 0;
while i < 4 {
    let j = 0;
    while true {
        j = j + 1;
        if j == 3 { break; }
    }
    total = total + j;
    i = i + 1;
}
print total;                 # 12
print 0 and 1 / 0;           # 0: 右支除零被短路
print 1 or 1 / 0;            # 1: 右支除零被短路
if total == 12 { print 100; } else { print 200; }
"""


def section(title: str) -> None:
    print()
    print("=" * 64)
    print(title)
    print("=" * 64)


def main() -> None:
    section("1. 样例源码")
    print(SAMPLE.rstrip())

    code = compile_program(parse(SAMPLE))

    section("2. 字节码反汇编 (跳转目标为字节偏移)")
    print(isa.disassemble(code))

    section("3. 静态验证 (每条指令入口的栈高度)")
    heights = verify(code)
    for pc, (info, operand, _nxt) in isa.decode_all(code).items():
        suffix = f" {operand}" if operand is not None else ""
        height = heights.get(pc)
        shown = str(height) if height is not None else "不可达"
        print(f"  pc={pc:<4} 栈高={shown:<4} {info.name}{suffix}")
    print(f"  -> 验证通过, 共 {len(code)} 字节, {len(heights)} 个指令边界")

    section("4. 完整运行")
    machine = Machine(code)
    output = machine.run()
    print("  print 输出:", output)
    print("  执行指令数:", machine.steps)

    section("5. 单步执行 (print 2 + 3; 前 4 步)")
    small = compile_program(parse("print 2 + 3;"))
    stepper = Machine(small)
    expected = ["PUSH 2", "PUSH 3", "ADD", "PRINT"]
    for i in range(4):
        pc = stepper.pc
        stepper.step()
        print(f"  第 {i + 1} 步: pc={pc} {expected[i]}, 栈={stepper.stack}, 输出={stepper.output}")

    section("6. 指令预算 (while true {} 预算=20)")
    infinite = compile_program(parse("while true { }"))
    runaway = Machine(infinite, budget=20)
    try:
        runaway.run()
    except BudgetExhausted as exc:
        print(f"  拒绝执行: {exc}")
        print(f"  实际执行: {runaway.steps} 条指令, pc={runaway.pc}")

    section("7. 人工损坏: 把 JZ 目标改成 PUSH 指令的中间字节")
    decoded = isa.decode_all(code)
    jz_pc = next(pc for pc, (i, _, _) in decoded.items() if i.name == "JZ")
    push_pc = next(pc for pc, (i, _, _) in decoded.items() if i.name == "PUSH")
    corrupted = bytearray(code)
    bad_target = push_pc + 1  # 5 字节 PUSH 指令的第 2 个字节
    corrupted[jz_pc + 1 : jz_pc + 3] = bad_target.to_bytes(2, "big")
    print(f"  pc={jz_pc} 处 JZ 原目标为正常边界, 现改为 {bad_target}")
    try:
        verify(bytes(corrupted))
    except VerifyError as exc:
        print(f"  验证器拒绝: {exc}")


if __name__ == "__main__":
    main()
