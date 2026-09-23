"""运行样例：最小括号打印、结构往返、精确有理求值、歧义表拒绝。

用 Python 3.11 运行：python3.11 demo.py
"""

from fractions import Fraction

from exprlib import (
    FRAC_SLASH,
    Grammar,
    GrammarError,
    evaluate,
    parse,
    to_string,
)


SAMPLES = [
    "a-(b-c)",
    "a-b-c",
    "(-x)^2",
    "-x^2",
    "2^3^2",
    "(2^3)^2",
    "a?b:c?d:e",
    "(a?b:c)?d:e",
    "f(g(a+b), c?d:e)",
    "-(a*b)",
    "x^-(a+b)",
    "-(-2" + FRAC_SLASH + "3)",
]


def demo_minimal_parentheses():
    print("== 最小必要括号（结构定义，非数值等价） ==")
    for text in SAMPLES:
        tree = parse(text)
        printed = to_string(tree)
        same = parse(printed) == tree
        print(f"  {text:<24} => {printed:<22} 往返={'是' if same else '否'}")
    print()
    print("  结构区分：parse('a-(b-c)') != parse('a-b-c') ->",
          parse("a-(b-c)") != parse("a-b-c"))
    print("  结构区分：parse('(-x)^2') != parse('-x^2')   ->",
          parse("(-x)^2") != parse("-x^2"))


def demo_exact_arithmetic():
    print("\n== 精确有理数（无浮点） ==")
    third = "1" + FRAC_SLASH + "3"
    expr = " + ".join([third] * 3)
    value = evaluate(parse(expr))
    print(f"  {expr} = {value!r}（类型 {type(value).__name__}，恰好等于 1：{value == 1}）")
    print(f"  (1{FRAC_SLASH}2)^-2 = {evaluate(parse('(1' + FRAC_SLASH + '2)^-2'))}")
    print(f"  2^64 = {evaluate(parse('2^64'))}")
    print("  x ? 1/x : 42 @ x=0 ->",
          evaluate(parse("x ? 1 / x : 42"), env={"x": 0}))
    print("  sq(3/2), add(...)= ",
          evaluate(parse("add(sq(3" + FRAC_SLASH + "2), 3" + FRAC_SLASH + "2)"),
                   functions={"sq": lambda z: z * z,
                              "add": lambda u, v: u + v}))


def demo_custom_grammar():
    print("\n== 可配置优先级表与歧义拒绝 ==")
    g = Grammar.build([("..", 1, "L"), ("++", 2, "R")],
                      unary_symbol="~", unary_prec=3)
    print("  自定义表解析 a..b..c =>", to_string(parse("a..b..c", g), g))
    for bad in (
        [("+", 2, "L"), ("*", 2, "R")],          # 同级混合结合性
        [("-", 5, "L"), ("+", 1, "L")],          # 左结合且比一元更紧
        [("+", 1, "L"), ("+", 2, "L")],          # 重复符号
    ):
        try:
            Grammar.build(bad, unary_symbol="-", unary_prec=3)
        except GrammarError as exc:
            print(f"  拒绝表 {bad}: {exc}")


if __name__ == "__main__":
    demo_minimal_parentheses()
    demo_exact_arithmetic()
    demo_custom_grammar()
