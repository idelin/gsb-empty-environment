"""运行样例: 覆盖精确有理数、条件规则、循环检测、步数耗尽与回放。"""
from rewriter import (
    Engine, PLit, PVar, Rule, Symbol, papp,
    parse, render, render_result, standard_rules,
)


def banner(title):
    print("=" * 64)
    print(title)
    print("=" * 64)


def demo_arithmetic():
    banner("样例 1: 有理数精确化简(完整变换链, 每步含位置与适用域)")
    engine = Engine(standard_rules())
    result = engine.rewrite(parse("add(add(1/2, 1/3), mul(2/3, 3/4))"))
    print(render_result(result))
    final, ok = engine.replay(result.initial, result.steps)
    print(f"回放校验: {'一致' if ok and final == result.final else '不一致'}")


def demo_conditions():
    banner("样例 2: 条件规则(不满足条件的规则不可用)")
    engine = Engine(standard_rules())
    print(render_result(engine.rewrite(parse("abs(-3/4)"))))
    print()
    print("div(mul(x, 0), 0): div-cancel 要求 y ≠ 0, 条件不满足 -> 不可用;")
    print("div(0, 0): div-num 要求 b ≠ 0, 条件不满足 -> 不可用")
    print(render_result(engine.rewrite(parse("div(mul(x, 0), 0)"))))


def demo_cycle():
    banner("样例 3: 循环检测(a -> b, b -> a)")
    rules = [
        Rule("a-to-b", PLit(Symbol("a")), PLit(Symbol("b")),
             domain="a ∈ {a, b}"),
        Rule("b-to-a", PLit(Symbol("b")), PLit(Symbol("a")),
             domain="b ∈ {a, b}"),
    ]
    result = Engine(rules).rewrite(Symbol("a"))
    print(render_result(result))


def demo_exhaustion():
    banner("样例 4: 步数耗尽(规则使表达式无限增长)")
    rules = [Rule("grow", papp("s", PVar("x")),
                  papp("s", papp("s", PVar("x"))),
                  domain="任意表达式 x")]
    result = Engine(rules).rewrite(parse("s(a)"), max_steps=4)
    print(render_result(result))


def demo_priority():
    banner("样例 5: 规则优先级(高优先级规则先适用)")
    rules = [
        Rule("generic-f", papp("f", PVar("x")), PLit(Symbol("g")),
             priority=1, domain="任意 x"),
        Rule("specific-f-a", papp("f", PLit(Symbol("a"))),
             PLit(Symbol("h")), priority=10, domain="x = a"),
    ]
    result = Engine(rules).rewrite(parse("f(a)"))
    print(render_result(result))


if __name__ == "__main__":
    demo_arithmetic()
    print()
    demo_conditions()
    print()
    demo_cycle()
    print()
    demo_exhaustion()
    print()
    demo_priority()
