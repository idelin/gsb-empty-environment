"""运行样例：有理函数 GCD 约分 + 定义域排除集合 + 独立验证。"""
from fractions import Fraction

from polyutil import poly, mul, fmt_poly, squarefree_factorization
from rational import RationalFunction, DomainError, fmt_exclusions
from verify import full_verify


def show(title, rf, probe_points=()):
    print("=" * 72)
    print(title)
    print("-" * 72)
    print(f"原式：  ({fmt_poly(rf.original_num)}) / ({fmt_poly(rf.original_den)})")
    print(f"化简：  ({fmt_poly(rf.num)}) / ({fmt_poly(rf.den)})")
    print(f"排除集：{fmt_exclusions(rf.exclusions)}")
    print("变换历史（每步附适用域）：")
    for i, h in enumerate(rf.history, 1):
        print(f"  {i}. {h}")
    result = full_verify(rf)
    print(f"独立验证：{result}")
    for x in probe_points:
        try:
            print(f"  f({x}) = {rf.evaluate(x)}"
                  f"（原始形式 {rf.evaluate_original(x)}，一致）")
        except DomainError as e:
            print(f"  f({x}) -> DomainError: {e}")


def main():
    # 1. x / x -> 1，但仍排除 x = 0
    show("样例 1：x / x（可去奇点保留）",
         RationalFunction([0, 1], [0, 1]),
         probe_points=[0, 3])

    # 2. 重根公共因子：(x-1)^2 (x+2) / (x-1)^3
    xm1 = poly([-1, 1])
    num = mul(mul(xm1, xm1), poly([2, 1]))
    den = mul(mul(xm1, xm1), xm1)
    show("样例 2：(x-1)^2 (x+2) / (x-1)^3（重根约分）",
         RationalFunction(num, den),
         probe_points=[1, 0, Fraction(3, 2)])

    # 3. 无理排除点：(x^2-2)(x-1) / (x^2-2)，可去奇点为 ±sqrt(2)
    x2m2 = poly([-2, 0, 1])
    show("样例 3：(x^2-2)(x-1) / (x^2-2)（无理可去奇点，隔离区间描述）",
         RationalFunction(mul(x2m2, poly([-1, 1])), x2m2),
         probe_points=[Fraction(3, 2), Fraction(1414213, 1000000), 2])

    # 4. 零函数：0 / (x-1)
    show("样例 4：0 / (x-1)（零函数保留排除集）",
         RationalFunction([], [-1, 1]),
         probe_points=[1, 5])

    # 5. 分母首项符号规范：1 / (-2x + 4)
    show("样例 5：1 / (-2x + 4)（首项符号规范为首一）",
         RationalFunction([1], [4, -2]),
         probe_points=[2, 0])

    # 6. 组合运算更新排除集合：1/(x-1) + 1/(x-2)
    f = RationalFunction([1], [-1, 1])
    g = RationalFunction([1], [-2, 1])
    show("样例 6：1/(x-1) + 1/(x-2)（组合后并集排除）",
         f + g, probe_points=[1, 2, 0])

    # 7. 乘法约分仍保留双方排除点
    h = RationalFunction([-1, 1], [-2, 1])       # (x-1)/(x-2)
    k = RationalFunction([-2, 1], [-3, 1])       # (x-2)/(x-3)
    show("样例 7：(x-1)/(x-2) * (x-2)/(x-3)（乘积约分不丢排除点）",
         h * k, probe_points=[2, 3, 0])

    # 8. 平方自由分解演示
    print("=" * 72)
    print("样例 8：平方自由分解 (x-1)^3 (x+2)^2 (x-3)")
    p = poly([-1, 1])
    for _ in range(2):
        p = mul(p, poly([-1, 1]))
    p = mul(p, mul(poly([2, 1]), poly([2, 1])))
    p = mul(p, poly([-3, 1]))
    lead, facs = squarefree_factorization(p)
    print(f"  首项系数 {lead}；因子：" +
          ", ".join(f"({fmt_poly(fac)})^{m}" for fac, m in facs))

    # 9. 零分母拒绝
    print("=" * 72)
    print("样例 9：零分母拒绝")
    try:
        RationalFunction([1], [])
    except ValueError as e:
        print(f"  ValueError: {e}")


if __name__ == "__main__":
    main()
