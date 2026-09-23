"""运行样例：python3.11 main.py"""
from fractions import Fraction

from diffdag import Factory, Differentiator, Simplifier, domain_of, fmt_domain, to_str


def analyze(title, f_expr, factory, var="x"):
    print(f"=== {title} ===")
    print(f"f({var}) = {to_str(f_expr)}")
    dom = domain_of(f_expr)
    print(f"自然定义域: {fmt_domain(dom)}")

    differ = Differentiator(factory, var)
    df = differ.diff(f_expr)
    print("求导步骤（来源规则 / 适用域）:")
    for i, s in enumerate(differ.steps, 1):
        print(f"  {i:2d}. [{s.rule}] d/d{var}({s.node_str}): {s.detail}    适用域: {s.domain}")
    print(f"共享子图复用次数: {differ.reuse_count}")

    simp = Simplifier(factory)
    dfs = simp.simplify(df)
    print("化简步骤:")
    if simp.steps:
        for i, st in enumerate(simp.steps, 1):
            print(f"  {i:2d}. [{st.rule}] {st.before}  ⇒  {st.after}    适用域: {st.domain}")
    else:
        print("  （无需化简）")

    # 导数定义域 = 原函数定义域 ∩ 导数表达式的自然定义域（化简不改变定义域）
    conds = list(dom)
    for c in domain_of(dfs):
        if c not in conds:
            conds.append(c)
    print(f"f'({var}) = {to_str(dfs)}")
    print(f"导数定义域: {fmt_domain(tuple(conds))}")
    print()


def main():
    # 例 1：多项式，精确有理数系数
    fac = Factory()
    x = fac.var("x")
    f1 = fac.add(fac.add(fac.pow(x, 3), fac.mul(fac.const(Fraction(1, 3)), fac.pow(x, 2))),
                 fac.add(fac.mul(fac.const(-2), x), fac.const(7)))
    analyze("例 1：多项式（精确有理数）", f1, fac)

    # 例 2：乘积法则 + 链式法则（初等函数）
    fac = Factory()
    x = fac.var("x")
    f2 = fac.add(fac.mul(x, fac.sin(x)), fac.cos(x))
    analyze("例 2：x·sin(x) + cos(x)", f2, fac)

    # 例 3：对数，导数保留 x > 0 定义域
    fac = Factory()
    x = fac.var("x")
    f3 = fac.log(x)
    analyze("例 3：log(x)，定义域保留", f3, fac)

    # 例 4：共享子图 y = x^2+1 被引用 3 次，只求导一次
    fac = Factory()
    x = fac.var("x")
    y = fac.add(fac.pow(x, 2), fac.const(1))
    f4 = fac.mul(fac.mul(y, y), y)
    analyze("例 4：共享子图 y=x^2+1，f = y·y·y", f4, fac)

    # 例 5：深层共享 DAG：y1=x^2+1, y2=y1·y1+y1, y3=y2·y2+y2
    fac = Factory()
    x = fac.var("x")
    y1 = fac.add(fac.pow(x, 2), fac.const(1))
    y2 = fac.add(fac.mul(y1, y1), y1)
    y3 = fac.add(fac.mul(y2, y2), y2)
    analyze("例 5：深层共享 DAG（指数级结构不重复展开）", y3, fac)
    print(f"全仓库 DAG 节点总数（哈希一致化后）: {fac.node_count}")
    print()

    # 例 6：特殊点语义：x^0、x^(-2)、x^(1/2)
    fac = Factory()
    x = fac.var("x")
    f6 = fac.add(fac.add(fac.pow(x, 0), fac.pow(x, -2)), fac.pow(x, Fraction(1, 2)))
    analyze("例 6：x^0 + x^(-2) + x^(1/2)，特殊点语义", f6, fac)


if __name__ == "__main__":
    main()
