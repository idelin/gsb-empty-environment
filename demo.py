"""运行样例：多变量稀疏多项式库功能演示（Python 3.11，仅标准库）。

每一步变换均打印其适用域。运行：python3.11 demo.py
"""
from __future__ import annotations

import random
import tempfile
from fractions import Fraction

from poly import Polynomial, TermLimitExceeded, multiply
from lazy import Add, Budget, Deriv, Mul, Partial, evaluate, evaluate_full, leaf, resume
from dense_ref import DenseModP, sparse_mod_p

LINE = "=" * 68


def show(title, domain):
    print(f"\n--- {title}")
    print(f"    适用域: {domain}")


def main():
    print(LINE)
    print("多变量稀疏多项式演示（有理数精确系数，指数向量存储，仅标准库）")
    print(LINE)

    # 1. 构造：合并同类项 + canonical 排序
    show("1. 构造：重复单项式精确合并、canonical 排序",
         "任意整环上的单项式表示；序为分级字典序")
    p = Polynomial(2, [((1, 0), 1), ((1, 0), Fraction(1, 2)),
                       ((0, 2), 3), ((0, 0), -1)])
    print(f"    p = {p.to_string()}   （项数 {p.term_count}）")

    # 2. x - x = 0，零项回收
    show("2. 减法：x - x 精确得零多项式，零项回收", "任意环（加法逆元）")
    x = Polynomial.variable(0, 2)
    z = x - x
    print(f"    x0 - x0 = {z.to_string()}   is_zero={z.is_zero()} 项数={z.term_count}")

    # 3. 加法与乘法（交叉项合并）
    show("3. 乘法：交叉项精确合并 (x+y)(x-y) = x^2 - y^2", "任意交换环（分配律）")
    y = Polynomial.variable(1, 2)
    prod = (x + y) * (x - y)
    print(f"    (x0 + x1) * (x0 - x1) = {prod.to_string()}   （项数 {prod.term_count}）")

    # 4. 偏导与交换关系
    show("4. 偏导：d/dx0 d/dx1 == d/dx1 d/dx0", "特征 0 整环（此处为 Q）")
    f = Polynomial(2, {(2, 1): Fraction(3), (0, 3): Fraction(1, 2), (0, 0): 2})
    print(f"    f = {f.to_string()}")
    print(f"    df/dx0 = {f.deriv(0).to_string()}")
    print(f"    df/dx1 = {f.deriv(1).to_string()}")
    print(f"    d2f/dx0dx1 = {f.deriv(0).deriv(1).to_string()}"
          f" == d2f/dx1dx0 = {f.deriv(1).deriv(0).to_string()}")

    # 5. 负指数拒绝 / 零变量常数
    show("5. 负指数拒绝；零变量常数有定义", "构造期校验；任意含单位元环")
    try:
        Polynomial(2, {(-1, 0): 1})
    except ValueError as exc:
        print(f"    负指数 -> ValueError: {exc}")
    c0 = Polynomial.constant(Fraction(3, 4), 0)
    print(f"    零变量常数 c = {c0.to_string()}，c*c = {(c0 * c0).to_string()}")

    # 6. 内存额度 + 外存归并
    show("6. 稀疏乘法：内存额度受限时外存归并", "任意交换环；归并段排序 + k 路归并")
    rng = random.Random(2026)
    def rp(nterms, nvars=3, deg=3):
        return Polynomial(nvars, {
            tuple(rng.randint(0, deg) for _ in range(nvars)):
                Fraction(rng.randint(-5, 5), rng.randint(1, 3))
            for _ in range(nterms)
        })
    a, b = rp(12), rp(12)
    full = multiply(a, b)                      # 默认大额度，纯内存
    with tempfile.TemporaryDirectory() as d:
        ext = multiply(a, b, mem_budget=8, spill_dir=d)   # 强制外存归并
    print(f"    a 项数={a.term_count}, b 项数={b.term_count}, "
          f"乘积项数={full.term_count}")
    print(f"    外存归并(mem_budget=8) 与内存结果一致: {ext == full}")
    print(f"    canonical 输出一致: {ext.to_string() == full.to_string()}")

    # 7. 惰性展开：预算不足返回可继续表达式
    show("7. 惰性节点：预算不足返回 Partial（可继续），非截断",
         "分配律/求导线性性的恒等改写，特征 0 交换环")
    expr = Add(Deriv(Mul(leaf(a), leaf(b)), 1), leaf(a))
    direct = (a * b).deriv(1) + a
    r = evaluate(expr, Budget(quota=5))
    print(f"    quota=5 首切片 -> {'Partial(可继续)' if isinstance(r, Partial) else '完成'}"
          f"，已累积项数={r.acc.term_count if isinstance(r, Partial) else r.term_count}")
    final, peak, slices = evaluate_full(expr, quota=5)
    print(f"    quota=5 连续恢复 -> 完成，切片数={slices}，峰值项数={peak}")
    print(f"    与一次性求值一致: {final == direct}")

    # 8. 随机暂停恢复：canonical 输出不变 + 峰值项数记录
    show("8. 随机暂停/恢复：canonical 输出不变，记录峰值项数",
         "恒等改写保持数学对象不变，canonical 形式唯一")
    rng2 = random.Random(99)
    peaks = []
    ok = True
    for _ in range(10):
        quota = rng2.randint(1, 7)
        f2, pk, sl = evaluate_full(expr, quota=quota)
        ok &= (f2.to_string() == direct.to_string())
        peaks.append((quota, pk, sl))
    print(f"    10 组随机配额 canonical 全部一致: {ok}")
    print(f"    (quota, 峰值项数, 切片数) 样例: {peaks[:4]}")

    # 9. 项数膨胀硬上限
    show("9. 项数膨胀硬上限：超过即 TermLimitExceeded", "纯记账闸门，无代数假设")
    try:
        evaluate_full(Mul(leaf(a), leaf(b)), quota=10, max_terms=4)
    except TermLimitExceeded as exc:
        print(f"    max_terms=4 -> TermLimitExceeded: {exc}")

    # 10. 独立稠密小域参考交叉检验
    show("10. 独立稠密 GF(101) 参考：交叉检验运算与偏导交换",
         "GF(p) 稠密数组 vs Q 稀疏字典，两种表示互验")
    da = DenseModP.from_sparse(a, 101, 12)
    db = DenseModP.from_sparse(b, 101, 12)
    add_ok = sparse_mod_p(a + b, 101) == (da + db).to_dict()
    mul_ok = sparse_mod_p(a * b, 101) == (da * db).to_dict()
    der_ok = sparse_mod_p((a * b).deriv(0), 101) == (da * db).deriv(0).to_dict()
    comm_ok = da.deriv(0).deriv(1) == da.deriv(1).deriv(0)
    print(f"    加法一致={add_ok}  乘法一致={mul_ok}  乘积偏导一致={der_ok}")
    print(f"    稠密侧偏导交换 d/dx0 d/dx1 == d/dx1 d/dx0: {comm_ok}")

    print("\n" + LINE)
    print("演示结束。运行测试：python3.11 -m unittest test_poly -v")
    print(LINE)


if __name__ == "__main__":
    main()
