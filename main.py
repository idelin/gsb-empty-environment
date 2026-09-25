"""运行样例：多层位置映射、诊断回溯、宏更新后的局部重编译与旧诊断查询。

运行: python3.11 main.py
"""
from posmap.session import Session

PROGRAM = """# 演示程序（含中文标识符，位置按 UTF-8 字节计）
let 甲 = 3;
let b = SQUARE(甲) + 1;
print b;
print b / (甲 - 3);
"""

MACROS = {
    "SQUARE": "SQUARE(x) = x * x",
    "DOUBLE": "DOUBLE(x) = x + x",
}


def main():
    session = Session(PROGRAM, MACROS)

    print("=== 展开后文本 (expanded 层) ===")
    print(session.expanded_text)

    print("=== 字节码及其最佳来源（pc -> 用户/宏片段） ===")
    for pc, ins in enumerate(session.instructions):
        ex = session.explain_pc(pc)
        if ex.best is None:
            loc = "(无来源，不伪造行号)"
        else:
            loc = f"[{ex.best.role}] {session.format_span(ex.version, ex.best.span)}"
        arg = "" if ins.arg is None else f" {ins.arg}"
        print(f"{pc:3} {ins.op:6}{arg:<6} <- {loc}")

    print()
    print("=== 第 1 次运行（v0）：触发除零 ===")
    output, diag = session.run()
    print("已输出:", output)
    print(session.format_explanation(session.explain(diag)))

    print()
    print("=== 更新宏 SQUARE：只局部重编译受影响语句 ===")
    report = session.update_macro("SQUARE", "SQUARE(x) = (x + 1) * (x + 1)")
    print(f"版本: v{report.old_version} -> v{report.new_version}")
    print(f"重编译语句: {report.recompiled_statements}")
    print(f"触及映射块数: {report.touched_blocks}")
    print(f"仅平移坐标的复用块数: {report.shifted_blocks}")
    print(f"完全复用块数: {report.reused_blocks}")

    print()
    print("=== 旧诊断仍按旧快照 v0 解释（新坐标不套旧源文） ===")
    old_ex = session.explain(diag)
    print(f"解释使用版本: v{old_ex.version}，定位不变:")
    print(session.format_span(old_ex.version, old_ex.best.span))

    print()
    print("=== 第 2 次运行（v1）：新字节码，新诊断 ===")
    print("展开文本:")
    print(session.expanded_text)
    output2, diag2 = session.run()
    print("已输出:", output2)
    print(session.format_explanation(session.explain(diag2)))


if __name__ == "__main__":
    main()
