"""运行样例：展示增量加点、Hermite 重复节点、撤回、冲突诊断与位长增长。

直接运行：python3 demo.py
"""

from fractions import Fraction as F

from rational_interp import (
    Interpolator, Poly, DuplicateConflictError, StructureError,
    linear_solve)


def hr(title: str) -> None:
    print("\n" + "=" * 68)
    print(title)
    print("=" * 68)


def show_verify(itp: Interpolator) -> None:
    p = itp.polynomial()
    v = itp.verify(p)
    print(f"  规范幂基  P(x) = {p}")
    nodes, coeffs = itp.newton_form()
    print("  Newton 系数:", [f"{c}" for c in coeffs])
    print("  Newton 节点:", [f"{x}" for x in nodes])
    for (x, k), r in sorted(v.residuals.items(), key=lambda t: (t[0][0], t[0][1])):
        tag = f"P^({k})({x})" if k else f"P({x})"
        print(f"    核验 {tag} 残差 = {r}")
    rep = itp.reference_check()
    print(f"  独立线性求解器一致: {rep.consistent}；与差商展开相同: {rep.agree}")
    print("  分子/分母最大位长(bit):", itp.bits.report())


def main() -> None:
    hr("样例 1：增量加点，精确有理 Lagrange 插值（1/x 型数据）")
    itp = Interpolator()
    data = [(1, 1), (2, F(1, 2)), (4, F(1, 4))]
    for x, y in data:
        itp.observe(x, y)
        print(f"  加入 ({x}, {y}) 后: {itp.polynomial()}")
    show_verify(itp)

    hr("样例 2：重复 x 检查——同值幂等；不同 y 给冲突见证")
    itp.observe(4, F(1, 4))
    print("  再次加入相同的 (4, 1/4)：幂等，观测数仍为", len(itp.observations))
    try:
        itp.observe(4, F(1, 5))
    except DuplicateConflictError as e:
        print(f"  捕获冲突: {e}")
    print("  --- 独立求解器层面制造不相容超定系统并取见证 ---")
    A = [[F(1), x] for x, _ in data] + [[F(1), F(4)]]
    b = [y for _, y in data] + [F(1, 5)]
    res = linear_solve(A, b)
    print("  相容?", res.consistent, " 见证 w =", res.witness)
    wA = [sum(wi * ai for wi, ai in zip(res.witness, col)) for col in zip(*A)]
    wb = sum(wi * bi for wi, bi in zip(res.witness, b))
    print(f"  验证见证: w*A = {wA}（零行），w*b = {wb}（非零 => 不相容证书）")

    hr("样例 3：Hermite 重复节点导数约束，恢复 P(x)=x^3")
    h = Interpolator()
    h.observe(0, 0, 0)
    h.observe(0, 0, 1)   # P'(0)=0
    h.observe(0, 0, 2)   # P''(0)=0
    h.observe(1, 1, 0)
    h.observe(1, 3, 1)   # P'(1)=3
    h.observe(2, 8, 0)
    show_verify(h)

    hr("样例 4：缺低阶先给高阶 -> 结构错误")
    bad = Interpolator()
    try:
        bad.observe(3, 6, derivative_order=1)
    except StructureError as e:
        print(f"  捕获: {e}")

    hr("样例 5：撤回中间观测——复用前缀，不止删最后一个系数")
    w = Interpolator()
    for x, y in [(0, 1), (1, 3), (2, 9), (3, 27)]:
        w.observe(x, y)
    before = w.newton_coefficients
    w.forget(2)  # 撤回内部节点 x=2
    after = w.newton_coefficients
    print("  撤前 Newton 系数:", before)
    print("  撤后 Newton 系数:", after)
    print("  共享前缀长度:", next((i for i, (a, b) in enumerate(zip(before, after))
                                if a != b), min(len(before), len(after))))
    show_verify(w)
    print("  注意：撤后系数个数由 4 变 3，但第 2、3 个系数都需重算，"
          "证明不是简单删末项。")

    hr("样例 6b：条件不相容见证（合法 Hermite 系统被篡改一个右端值）")
    g = Interpolator()
    for x, y in [(0, 1), (1, 3), (2, 9)]:
        g.observe(x, y)
    A, b, keys, d = g._hermite_system()
    # 追加一条与已有二次插值矛盾的第 4 个函数值约束 -> 超定不相容
    A = A + [[F(1), F(3), F(9)]]
    bad_b = b + [F(100)]
    res = linear_solve(A, bad_b)
    print("  系统相容?", res.consistent, " 见证 w =", res.witness)
    print("  w*A =", [sum(wi * ai for wi, ai in zip(res.witness, col))
                      for col in zip(*A)])
    print("  w*b =", sum(wi * bi for wi, bi in zip(res.witness, bad_b)),
          "(非零即不相容证书)")

    hr("样例 6c：位长增长观察（分母较大的有理数据）")
    big = Interpolator()
    for i, y in enumerate([F(1, 2), F(3, 5), F(7, 11), F(13, 17), F(19, 23)]):
        big.observe(i, y)
    big.reference_check()
    for stage, (nb, db) in big.bits.report().items():
        print(f"    {stage:22s} 分子最大 {nb:3d} bit, 分母最大 {db:3d} bit")
    print("  全部残差:", set(big.verify().residuals.values()))

    hr("样例 7：节点加入顺序变化 -> 数学上同一个多项式")
    orders = [
        [(0, 1), (1, 3), (2, 9)],
        [(2, 9), (0, 1), (1, 3)],
        [(1, 3), (2, 9), (0, 1)],
    ]
    polys = set()
    for od in orders:
        q = Interpolator()
        for x, y in od:
            q.observe(x, y)
        polys.add(q.polynomial())
        print(f"  顺序 {od} -> {q.polynomial()}")
    print("  三种顺序产生的规范幂基完全相同:", len(polys) == 1)


if __name__ == "__main__":
    main()
