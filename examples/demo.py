"""运行样例：解析 -> 打印 -> 结构往返校验 -> 精确求值 -> 括号理由。

用法：python3.11 examples/demo.py
"""
import os
import sys
from fractions import Fraction

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from minparen import evaluate, parse, render, to_string  # noqa: E402

ENV = {"a": 5, "b": 3, "c": 1, "x": 2}
FUNCS = {"f": lambda u, v: u + v, "g": lambda u: u * u}

SAMPLES = [
    "a - (b - c)",
    "a - b - c",
    "-x ^ 2",
    "(-x) ^ 2",
    "2 ^ 3 ^ 2",
    "(2 ^ 3) ^ 2",
    "2 ^ (-1)",
    "a + b * c",
    "a * (b + c)",
    "a ? b : c ? 1 : 2",
    "f(a + b, g(c))",
    "0.1 + 0.2",
    "1 / 3 * 3",
]


def main():
    print("== 解析 -> 最小括号打印 -> 结构往返 -> 精确求值 ==")
    for src in SAMPLES:
        tree = parse(src)
        out = to_string(tree)
        assert parse(out) == tree, "结构往返失败"
        try:
            val = evaluate(tree, env=ENV, functions=FUNCS)
            val_s = f"{val}（即 {val.numerator}/{val.denominator}）" \
                if val.denominator != 1 else str(val)
        except Exception as exc:  # 演示：越出适用域
            val_s = f"<{type(exc).__name__}: {exc}>"
        mark = "" if out == src else "  <- 打印结果与输入不同（括号已最小化）"
        print(f"  输入: {src:<22} 打印: {out:<22} 值: {val_s}{mark}")

    print()
    print("== 每对括号的理由（适用域说明）==")
    for src in ["a - (b - c)", "(-x) ^ 2", "2 ^ (-1)", "(a ? b : c) ? x : 1"]:
        r = render(parse(src))
        print(f"  {src}")
        for reason in r.reasons:
            print(f"    - {reason}")

    print()
    print("== 精确性对照 ==")
    exact = evaluate(parse("0.1 + 0.2"))
    print(f"  0.1 + 0.2 = {exact}（精确）；浮点结果会是 {0.1 + 0.2!r}")
    print(f"  1 / 3 * 3 = {evaluate(parse('1 / 3 * 3'))}（精确）")
    print(f"  Fraction('0.1') = {Fraction('0.1')}，字面量无浮点损失")


if __name__ == "__main__":
    main()
