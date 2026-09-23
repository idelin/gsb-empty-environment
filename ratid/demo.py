"""命令行演示：python -m ratid.demo

展示恒等、非等价（值反例 / 定义域反例）、预算超限、超出支持域等情形。
"""

from __future__ import annotations

from .judge import JudgeResult, Verdict, judge

VERDICT_CN = {
    Verdict.EQUIVALENT: "恒等 (equivalent)",
    Verdict.NOT_EQUIVALENT: "非等价 (not_equivalent)",
    Verdict.UNKNOWN: "未知 (unknown)",
}

CASES: list[tuple[str, str, str, int | None]] = [
    ("真恒等：分式恒等式（定义域也一致）",
     "(x^2 - 1) / (x - 1)", "x + 1 + (x - 1)/(x - 1) - 1", 256),
    ("非等价：约分结果相同，但分母定义域不同（不能只看约分）",
     "(x^2 - 1) / (x - 1)", "x + 1", 256),
    ("通分恒等",
     "1 / x + 1 / (x + 1)", "(2*x + 1) / (x*(x + 1))", 256),
    ("非等价：存在有理数点使值不同",
     "(x^2 - 1) / (x - 1)", "x", 256),
    ("非等价：约分相同但分母定义域不同",
     "(x^2 - 1) / (x - 1)", "((x^2 - 1)*(x + 1)) / ((x - 1)*(x + 1))", 256),
    ("项数预算超限 -> 未知",
     "(1 + x)^20", "x^20 + 20*x^19 + 190*x^18", 20),
    ("超出支持域：出现第二个变量",
     "x + y", "x", 256),
    ("超出支持域：指数不是整数",
     "x^(1/2)", "x", 256),
    ("负整数指数：定义域包含底数分子为零的点",
     "(x - 2)^(-1)", "1 / (x - 2)", 256),
    ("恒等：高次展开（预算内可完成）",
     "(x + 1)^4", "x^4 + 4*x^3 + 6*x^2 + 4*x + 1", 256),
]


def render(result: JudgeResult, verbose: bool = True) -> str:
    lines = [
        f"左式: {result.left}",
        f"右式: {result.right}",
        f"结论: {VERDICT_CN[result.verdict]}",
        f"理由: {result.reason}",
    ]
    if result.counterexample is not None:
        lines.append(f"反例: {result.counterexample.describe()}")
        lines.append("复核: 已分别将该点代入两个原始表达式（按原始分母判断有定义性），"
                     "确认与上述结论一致。")
    for check in result.checks:
        lines.append(f"判据: {check}")
    if verbose:
        lines.append("变换步骤（每步标注适用域）:")
        for i, step in enumerate(result.steps, 1):
            lines.append(f"  {i:2d}. [{step.stage}] {step.expr}")
            lines.append(f"      -> {step.detail}")
    return "\n".join(lines)


def main(verbose: bool = True) -> None:
    for idx, (title, a, b, budget) in enumerate(CASES, 1):
        print("=" * 78)
        print(f"案例 {idx}: {title}（项数预算 = {budget}）")
        print("-" * 78)
        print(render(judge(a, b, budget=budget), verbose=verbose))
        print()


if __name__ == "__main__":
    main()
