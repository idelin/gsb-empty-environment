"""运行样例：覆盖精确有理数、优先级、条件、循环、步数上限与回放。

用法：python3 examples/demo.py   或   python3 -m examples.demo
"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from symrewrite import (
    Add, Div, Mul, Num, Pow, Sym,
    default_rules, find_blocked, replay, rewrite, swap_rules, to_str,
    CYCLE, NORMAL_FORM, STEP_LIMIT,
)

STATUS_TEXT = {
    NORMAL_FORM: "normal_form（无可用规则，已达范式）",
    CYCLE: "cycle（检测到重复规范状态，存在循环）",
    STEP_LIMIT: "step_limit（步数耗尽）",
}


def show(title, result, rules):
    print(f"【{title}】")
    print(f"  初始: {to_str(result.initial)}")
    for s in result.steps:
        binds = ", ".join(f"{k}={v}" for k, v in s.bindings) or "（无）"
        print(f"  步骤 {s.index} | 规则 {s.rule} | 位置 {s.position} | 绑定 {binds}")
        print(f"         适用域: {s.domain}")
        if s.blocked:
            print(f"         条件不满足被拦下: {', '.join(s.blocked)}")
        print(f"         {to_str(s.before)}  ⇒  {to_str(s.after)}")
    print(f"  终止: {STATUS_TEXT[result.status]}")
    if result.cycle:
        first, again = result.cycle
        print(f"  循环: 状态首次出现于第 {first} 步，第 {again} 步重复")
    print(f"  结果: {to_str(result.final)}")
    replayed = replay(result.initial, result.steps, rules)
    ok = replayed == result.final
    print(f"  回放校验: {'通过' if ok else '失败'}")
    assert ok
    print()


def main():
    rules = default_rules()

    e1 = Mul(Add(Num("1/2"), Num("1/3")), Div(Sym("y"), Sym("y")))
    show("例1 精确有理数 + 完整变换链", rewrite(e1, rules), rules)

    show("例2 优先级：fold-add(20) 先于 double-add(5)",
         rewrite(Add(Num(1), Num(1)), rules), rules)

    show("例3 同类项合并 double-add",
         rewrite(Add(Sym("x"), Sym("x")), rules), rules)

    e4 = Div(Num(1), Num(0))
    r4 = rewrite(e4, rules)
    show("例4 条件不满足则规则不可用（除零）", r4, rules)
    for pos, name in find_blocked(r4.final, rules):
        rule = next(r for r in rules if r.name == name)
        print(f"  不可用: 规则 {name} 在位置 {pos} 被条件拦下（适用域要求 {rule.domain}）")
    print()

    e5 = Pow(Sym("x"), Num(0))
    show("例5 幂的适用域 x^0=1 要求 x≠0", rewrite(e5, rules), rules)

    show("例6 循环检测：a→b、b→a",
         rewrite(Sym("a"), swap_rules(), max_steps=50), swap_rules())

    show("例7 步数耗尽（max_steps=1）", rewrite(e1, rules, max_steps=1), rules)


if __name__ == "__main__":
    main()
