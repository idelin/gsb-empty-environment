"""运行样例: 有理系数因子提取 DAG 的精确/有界搜索与验证证书。"""
from polyfactor import factor_polynomial, parse
from polyfactor.poly import Poly


def show(result):
    print(result.render())
    print("-" * 72)


def demo_content_mechanics():
    """6x^2*y + 9*x*y^2 提 3xy: 展示内容提取机制与乘回核验。"""
    poly = parse("6*x^2*y + 9*x*y^2")
    c, m = poly.content()
    q = poly.divide_by_content(c, m)
    cm = Poly.monomial(poly.variables, c, m)
    print("内容提取机制演示 (6*x^2*y + 9*x*y^2 提 3xy):")
    print(f"  有理系数 gcd: {c}; 最小指数公共单项式: {cm}")
    print(f"  本原部分: {q}")
    print(f"  提取形式: {cm} * ({q})")
    print(f"  乘回严格等于原式: {cm * q == poly}")
    print("  适用域: 全体 R^2, 仅除以非零常数与非负指数单项式, 定义域限制不消失也不新增")
    print("-" * 72)


def main():
    demo_content_mechanics()

    print("样例 1: 经典提取 (优化器按运算次数全局比较, 不贪心)")
    show(factor_polynomial("6*x^2*y + 9*x*y^2"))

    print("样例 2: 递归分组真正降低运算次数")
    show(factor_polynomial("x^2 + x*y + x*z + y*z"))

    print("样例 3: 有理系数精确提取")
    show(factor_polynomial("1/2*x + 1/3*y + 1/2*x*y"))

    print("样例 4: 零多项式特殊处理")
    show(factor_polynomial("0", ))

    print("样例 5: 较大规模, 预算中断, 保留上下界")
    big = parse("(x + y + z)*(u + v + w)*(p + q + r)")
    print(f"  (展开后共 {len(big.terms)} 项)")
    show(factor_polynomial(big, budget=400))


if __name__ == "__main__":
    main()
