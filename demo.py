"""运行样例：同时代换 vs 顺序代换、零消项、有理数精确性、预算中断。

用法：python3.11 demo.py
"""
from simsub import (
    format_expr, format_poly, parse, replace_vars, substitute_string,
)


def show(title, result):
    print(f"### {title}")
    print(f"原式      : {format_expr(result.original)}")
    mapping = {k: format_expr(v) for k, v in result.mapping.items()}
    print(f"同时代换  : {mapping}")
    for i, step in enumerate(result.steps, 1):
        print(f"  步骤{i}    : {step.note}")
        print(f"    适用域: {step.domain}")
    if result.expanded:
        print(f"展开结果  : {format_poly(result.poly)}  （{result.terms} 项）")
    else:
        print(f"预算中断  : 估计项数上界 {result.estimate} > 预算 {result.budget}")
        print(f"未展开形式: {format_expr(result.result)}  （精确等式，非截断）")
    print()


def main():
    print("=" * 72)
    print("样例 1：x->y、y->x 同时代换 = 交换；顺序代换则被污染")
    print("=" * 72)
    r = substitute_string("x + 2*y", {"x": "y", "y": "x"})
    show("同时代换（正确：交换）", r)
    # 对照：顺序代换 x->y 再 y->x，全部变成 x
    seq = replace_vars(replace_vars(parse("x + 2*y"), {"x": parse("y")}),
                       {"y": parse("x")})
    print(f"对照·顺序代换: x->y 得 {format_expr(replace_vars(parse('x + 2*y'), {'x': parse('y')}))}"
          f"，再 y->x 得 {format_expr(seq)}  <- 顺序污染，全部塌缩成 x")
    print()

    print("=" * 72)
    print("样例 2：代换使因子为零 -> 零吸收律消去整项")
    print("=" * 72)
    r = substitute_string("(x - y)*z + x*y", {"x": "y"})
    show("x -> y，(y-y)*z 整项消去", r)

    print("=" * 72)
    print("样例 3：有理数精确计算（Fraction，无浮点误差）")
    print("=" * 72)
    r = substitute_string("(1/2)*x**2 + (2/3)*x*y - y**2", {"x": "y + 1/3"})
    show("x -> y + 1/3", r)

    print("=" * 72)
    print("样例 4：膨胀估计超预算 -> 停止展开，保留未展开的精确形式")
    print("=" * 72)
    r = substitute_string("(x + y + z)**10", {"x": "a + b"}, budget=100)
    show("(a+b+y+z)**10 保守上界 1001 > 预算 100", r)
    big = substitute_string("(x + y + z)**10", {"x": "a + b"}, budget=2000)
    print(f"对照·预算 2000 时正常展开：{big.terms} 项（保守上界 {big.estimate}，"
          f"上界偏保守是安全的，宁停勿错）")
    full = format_poly(big.poly)
    print(f"  展开式: {full[:160]} ...（共 {big.terms} 项，此处截断显示）")


if __name__ == "__main__":
    main()
