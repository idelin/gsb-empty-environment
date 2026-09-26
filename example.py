"""Executable demonstrations for earley_sppf.py."""

from earley_sppf import Grammar, Production


def show(title, grammar, tokens):
    print(f"\n{title}")
    print("规则:")
    for production in grammar.productions:
        print(f"  {production}")

    result = grammar.parse(tokens)
    label = "".join(tokens) if tokens else "ε"
    print(f"输入: {label!r}")
    print(f"接受: {result.accepted}")
    print(f"失败时最远位置: {result.farthest_position}")
    if result.accepted:
        print(f"森林摘要: {result.forest_summary()}")
    else:
        print("森林摘要: 无成功根节点")


def main():
    ambiguous = Grammar(
        alphabet=("a",),
        productions=(Production("S", ("S", "S")), Production("S", ("a",))),
        start_symbol="S",
    )
    show("左递归 + 歧义: S → SS | a, 输入 aaa", ambiguous, "aaa")

    epsilon = Grammar(
        alphabet=("a",),
        productions=(Production("S", ("S", "a")), Production("S", ())),
        start_symbol="S",
    )
    show("左递归 + epsilon: S → Sa | ε, 空输入", epsilon, ())

    failure = Grammar(
        alphabet=("a", "b"),
        productions=(Production("S", ("a", "a", "b")),),
        start_symbol="S",
    )
    show("失败最远位置: S → aab, 输入 aax", failure, "aax")


if __name__ == "__main__":
    main()
