"""运行样例：python3 demo.py"""
from fractions import Fraction

from differentiator import (
    ZERO, ONE, TWO,
    Const, Var, Add, Mul, Pow, Sin, Exp, Log,
    DiffEngine, Simplifier, count_nodes, to_str,
)

x = Var("x")


def run(title, f):
    print("=" * 72)
    print(title)
    print(f"f(x) = {to_str(f)}")
    eng = DiffEngine("x")
    df = eng.derive(f)
    print(f"求导计算的不同节点数: {eng.compute_count}（f 的 DAG 节点数: {count_nodes(f)}）")
    print("求导规则轨迹:")
    for i, t in enumerate(eng.trace, 1):
        print(f"  [{i}] {t.rule}")
        print(f"      作用于: {to_str(t.expr)}")
        print(f"      适用域: {t.domain}")
    simp = Simplifier()
    dfs = simp.simplify(df)
    print("化简步骤:")
    if not simp.steps:
        print("  （无需化简）")
    for i, s in enumerate(simp.steps, 1):
        print(f"  [{i}] {s.rule}")
        print(f"      {to_str(s.before)}  ==>  {to_str(s.after)}")
        print(f"      适用域: {s.domain}")
    print(f"f'(x) = {to_str(dfs)}")
    print()


def main():
    g = Add(Pow(x, TWO), ONE)  # 共享子图 x²+1

    run("样例 1：积规则 + 幂规则 + 链式规则，共享子图 x²+1 只求导一次",
        Mul(Pow(g, Const(3)), Sin(x)))

    run("样例 2：共享子图被高次幂引用，指数不展开",
        Add(Pow(g, Const(3)), Pow(g, TWO)))

    run("样例 3：ln 的导数保留 x>0 定义域，非整数次幂要求 u>0",
        Add(Log(x), Pow(x, Const(Fraction(1, 2)))))

    run("样例 4：常数幂 0 的语义（约定 a^0=1，含 0^0=1）",
        Add(Pow(x, ZERO), Pow(ZERO, ZERO)))

    run("样例 5：有理数精确计算",
        Add(Mul(Const(Fraction(1, 3)), Pow(x, Const(3))),
            Mul(Const(Fraction(1, 2)), Pow(x, TWO))))

    run("样例 6：广义幂规则（对数求导法）",
        Pow(x, x))

    run("样例 7：exp 与商结构（x·e^x + ln x）",
        Add(Mul(x, Exp(x)), Log(x)))


if __name__ == "__main__":
    main()
