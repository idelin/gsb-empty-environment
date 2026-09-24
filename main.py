"""演示：串联多个局部变换，逐步验证/回滚，最后做有限测试域差分验证。"""
from diffcheck import differential
from ir import format_program, parse, validate
from pipeline import run_pipeline
from transforms import (buggy_label_cleanup, eliminate_dead_writes,
                        fold_constants, remove_nops, remove_unreachable)

SAMPLE = """\
# 样例程序：输出 (a+b)*2、可折叠的常量表达式与倒计时
input x, 0
input y, 1
const two, 2
add s, x, y
mul s, s, two
print s
nop
const k1, 4
const k2, 5
add k3, k1, k2
print k3
const tmp, 99
const c, 3
loop:
print c
const one, 1
sub c, c, one
brnz c, loop
halt
const junk, 7
print junk
"""

TRANSFORMS = [
    ("常量折叠", fold_constants),
    ("删除空指令", remove_nops),
    ("死写消除", eliminate_dead_writes),
    ("有缺陷的标签清理（演示拦截）", buggy_label_cleanup),
    ("不可达代码删除", remove_unreachable),
]


def show_step(idx, report):
    status = "已应用" if report.applied else "已回滚（结构验证失败）"
    print(f"\n--- 步骤 {idx}: {report.name} -> {status} ---")
    print(f"指令数: {len(report.before)} -> {len(report.after)}"
          f"（候选 {len(report.candidate)} 条）")
    if report.errors:
        print("验证错误:")
        for err in report.errors:
            print(f"  * {err}")
        print("候选 IR（已丢弃）:")
        print(format_program(report.candidate))
    elif report.before != report.after:
        print("变换后 IR:")
        print(format_program(report.after))
    else:
        print("（IR 无变化）")


def main():
    original = parse(SAMPLE)
    print("=" * 64)
    print("原始 IR（含 UTF-8 字节区间源码位置）")
    print("=" * 64)
    print(format_program(original, show_spans=True))
    errors = validate(original)
    print(f"\n原始程序结构验证: {'通过' if not errors else errors}")

    final, reports = run_pipeline(original, TRANSFORMS)
    print("\n" + "=" * 64)
    print("变换流水线")
    print("=" * 64)
    for i, rep in enumerate(reports, 1):
        show_step(i, rep)

    print("\n" + "=" * 64)
    print("最终 IR")
    print("=" * 64)
    print(format_program(final))
    final_errors = validate(final)
    print(f"\n最终程序结构验证: {'通过' if not final_errors else final_errors}")

    print("\n" + "=" * 64)
    print("差分解释器：原始程序 vs 最终程序")
    print("=" * 64)
    result = differential(original, final)
    print(result.report())


if __name__ == "__main__":
    main()
