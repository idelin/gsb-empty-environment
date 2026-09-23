"""运行样例：sparsepoly 的核心功能演示。

运行：python3.11 demo.py
"""

import tempfile
from fractions import Fraction

from sparsepoly import (Add, Budget, Deriv, Expr, Leaf, Mul, Polynomial,
                        evaluate_fully)


def section(title, domain):
    print(f"\n=== {title} ===")
    print(f"    适用域: {domain}")


def main():
    x = Polynomial.variable(0, 2)
    y = Polynomial.variable(1, 2)

    section("基本运算与规范输出", "Q 上交换环（特征 0），精确有理数")
    p = (x + y) * (x + y)
    print(f"    (x+y)^2        = {p.to_string(['x', 'y'])}   # 交叉项合并为 2xy")
    print(f"    x - x          = {(x - x).to_string()}（零多项式，零项已回收）")
    q = Polynomial(2, {(1, 0): Fraction(1, 2), (0, 1): Fraction(1, 3)})
    print(f"    1/2 x + 1/3 y = {q.to_string(['x', 'y'])}")

    section("偏导", "特征 0；d(c*x^e)/dx_i = c*e_i*x^(e_i-1)")
    print(f"    d/dx (x+y)^2   = {p.partial(0).to_string(['x', 'y'])}")
    print(f"    d/dy d/dx      = {p.partial(0).partial(1).to_string(['x', 'y'])}"
          f" == d/dx d/dy = {p.partial(1).partial(0).to_string(['x', 'y'])}")

    section("边界情形", "负指数拒绝；零变量常数有定义")
    try:
        Polynomial(1, {(-1,): Fraction(1)})
    except ValueError as exc:
        print(f"    负指数被拒绝: {exc}")
    c = Polynomial.constant(Fraction(7, 2), nvars=0)
    print(f"    零变量常数     = {c.to_string()}  (terms={c.terms})")

    section("惰性求值：步数预算暂停/恢复", "断点不影响规范输出")
    f = (x + y) * (x + y) + Polynomial.constant(Fraction(1, 2), 2)
    g = x * y + y - Polynomial.constant(3, 2)
    expr = Add(Mul(Leaf(f), Leaf(g)), Deriv(Leaf(f), 0))
    node, calls = expr, 0
    while isinstance(node, Expr):
        budget = Budget(max_steps=5, allow_external=False)
        node = node.evaluate(budget)
        calls += 1
        if isinstance(node, Expr):
            print(f"    第 {calls} 次求值后暂停（步数额度 5 耗尽）-> {node!r}")
    print(f"    恢复完成，共 {calls} 次求值: {node.to_string(['x', 'y'])}")

    section("内存额度 + 外存归并", "单项式分级字典序全序；Q 上精确合并")
    big1 = f * f
    big2 = g * g
    print(f"    操作数规模: {len(big1.terms)} 项 x {len(big2.terms)} 项")
    with tempfile.TemporaryDirectory() as tmp:
        budget = Budget(max_live_terms=4, allow_external=True, spill_dir=tmp)
        result = evaluate_fully(Mul(Leaf(big1), Leaf(big2)), budget)
        print(f"    内存额度 4 项 -> 溢写运行段 {budget.spilled_runs} 个，"
              f"峰值项数 {budget.peak_live_terms}")
        print(f"    归并结果 {len(result.terms)} 项，"
              f"与直接乘法一致: {result == big1 * big2}")

    section("预算不足返回可继续表达式", "不截断、不丢项")
    node = Mul(Leaf(big1), Leaf(big2)).evaluate(
        Budget(max_live_terms=2, allow_external=False))
    print(f"    内存额度 2 且禁止外存 -> 返回 {node!r}（可继续，非截断）")
    done = evaluate_fully(node, Budget(max_live_terms=10_000,
                                       allow_external=False))
    print(f"    加大预算恢复后与直接乘法一致: {done == big1 * big2}")


if __name__ == "__main__":
    main()
