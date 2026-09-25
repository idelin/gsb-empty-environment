#!/usr/bin/env python3.11
"""端到端演示：

1. 自研词法/语法分析（源码位置为 UTF-8 字节区间）
2. 编译到栈式 IR
3. 串联多个局部变换，每步输出前后 IR 与检查结果
4. 结构验证失败 / 有限域差分不一致 -> 回滚该步，后续安全步骤照常运行
5. 差分解释器在有限测试域上验证语义等价（不声称全域证明）
"""

from minilang.lexer import tokenize
from minilang.parser import parse
from minilang.compiler import compile
from minilang.interp import run
from minilang.ir import format_ir
from minilang.pipeline import run_pipeline
from minilang.transforms import (
    AlgebraicPeephole,
    ConstantFold,
    ConstProp,
    UnsafeZeroAddPeephole,
)

SOURCE = """\
# 演示程序：累加 1..n；注释含多字节字符，验证字节区间
let n = input(0);
let zero = 0;
let sum = zero;
while n > 0 {
  sum = sum + n;
  n = n - 1;
}
let adjust = sum + (3 - 3);
if adjust > 0 {
  output adjust;
}
"""

DOMAIN = [(k,) for k in range(-2, 21)]  # 有限测试域：n ∈ [-2, 20]，共 23 个输入


def show_tokens(source: str) -> None:
    print("== 1. 词法分析（位置为 UTF-8 字节区间 [start, end)）==")
    tokens = tokenize(source)
    for tok in tokens[:6]:
        print(f"  {tok.kind:7s} {tok.text!r:12s} bytes [{tok.start}, {tok.end})")
    print("  ...")
    # 展示多字节注释之后的一个 token，证明偏移按字节而非字符计算
    let_tok = next(t for t in tokens if t.text == "let")
    prefix = source[: source.index("let")].encode("utf-8")
    print(f"  首个 'let' 的字节偏移 = {let_tok.start}，"
          f"其前缀的 UTF-8 编码长度 = {len(prefix)}（一致）")
    print()


def main() -> None:
    show_tokens(SOURCE)

    print("== 2. 语法分析 + 编译到 IR ==")
    program = parse(SOURCE)
    ir0 = compile(program)
    print(f"  AST 程序字节区间: [{program.span[0]}, {program.span[1]})")
    print(f"  编译得到 {len(ir0.instrs)} 条指令，输入槽位 {ir0.inputs} 个")
    print()

    print("== 3. 优化流水线（每步：前后 IR + 结构验证 + 有限域差分验证）==")
    transforms = [ConstantFold(), UnsafeZeroAddPeephole(), ConstProp(), AlgebraicPeephole()]
    result = run_pipeline(ir0, transforms, DOMAIN)
    for rec in result.records:
        verdict = "保留" if rec.kept else "回滚"
        print(f"  步骤 {rec.step}: {rec.pass_name} -> {verdict}")
        print(f"    原因: {rec.reason}")
        if rec.structural_errors:
            for err in rec.structural_errors:
                print(f"    结构错误: {err}")
        elif rec.diff_report is not None:
            print(f"    差分检查: {rec.diff_report.summary()}")
    print()

    bad = next(r for r in result.records if not r.kept)
    print("== 4. 被拦截步骤的前后 IR（坏变换破坏了分支目标）==")
    print("  --- 变换前 ---")
    print("  " + bad.before_ir.replace("\n", "\n  "))
    print("  --- 变换后（结构非法，已回滚）---")
    print("  " + bad.after_ir.replace("\n", "\n  "))
    print()

    print("== 5. 最终 IR（坏步骤已跳过，后续安全步骤仍生效）==")
    print("  " + format_ir(result.final).replace("\n", "\n  "))
    print(f"  保留的变换: {result.kept_passes}")
    print(f"  回滚的变换: {result.rolled_back_passes}")
    print()

    print("== 6. 在样例输入上运行最终 IR（与原始 IR 对照）==")
    for inputs in [(-1,), (0,), (5,), (10,)]:
        before = run(ir0, inputs)
        after = run(result.final, inputs)
        print(f"  input n={inputs[0]:>3}: 原始输出={before.outputs}  "
              f"优化后输出={after.outputs}  一致={before.observable() == after.observable()}")
    print()
    print("== 7. 验证范围声明 ==")
    print(f"  有限测试域: n ∈ [-2, 20]，共 {len(DOMAIN)} 个输入")
    print("  结论仅在该有限域内成立，不构成全域语义等价证明。")


if __name__ == "__main__":
    main()
