"""运行样例：分段有理函数的求和、乘法与连续性检查。

用法：python3.11 demo.py
"""

from fractions import Fraction

from ratpiece import Interval, PiecewiseModel, Poly, RationalFunc, check_continuity


def rf(num, den=(1,)):
    return RationalFunc(Poly(num), Poly(den))


def show_model(model):
    print(f"  分段（{len(model.pieces)} 段，边界 {[str(b) for b in model.boundaries]}）：")
    for piece in model.pieces_out():
        print(f"    {piece.interval.to_human():>12}  {piece.expr.to_human()}    <- {piece.source}")


def main():
    print("=" * 72)
    print("场景 1：分段函数求和 —— 自动细分交叠区间，空洞保留未定义")
    print("=" * 72)
    f = PiecewiseModel("f")
    f.set_piece(Interval.open(None, 1), RationalFunc.identity(), "f1: x")
    f.set_piece(Interval.closed(1, 3), RationalFunc.constant(2), "f2: 2")
    g = PiecewiseModel("g")
    g.set_piece(Interval.open(0, 5), rf((-1, 0, 1), (-1, 1)), "g1: (x^2-1)/(x-1)")
    print("f：")
    show_model(f)
    print("g（在 x=1 有可去空洞）：")
    show_model(g)
    h = PiecewiseModel.combine(f, g, "add", "h = f + g")
    print("h = f + g：")
    show_model(h)
    for x in (Fraction(1, 2), 1, 2, 4):
        print(f"  h({x}) = {h.evaluate(x)}")
    print()
    print("  变换日志（每步注明适用域）：")
    for piece in h.pieces_out():
        for step in piece.expr.steps:
            print(f"    [{piece.interval.to_human()}] {step}")
        break

    print()
    print("=" * 72)
    print("场景 2：乘法组合与连续性检查 —— 跳变、无穷间断、单侧定义")
    print("=" * 72)
    m = PiecewiseModel.combine(f, g, "mul", "m = f * g")
    print("m = f * g：")
    show_model(m)
    print()
    print(check_continuity(m).summary())
    print()
    print("  代数证据：")
    for b in check_continuity(m).boundaries:
        for line in b.evidence:
            print(f"    x={b.point}: {line}")

    print()
    print("=" * 72)
    print("场景 3：无理极点 —— Sturm 隔离 + 精确符号测试")
    print("=" * 72)
    k = PiecewiseModel("k")
    k.set_piece(Interval.open(-3, 3), rf((1,), (-2, 0, 1)), "k1: 1/(x^2-2)")
    rep = check_continuity(k)
    print(rep.summary())
    print()
    print("  证据：")
    for s in rep.singularities:
        for line in s.evidence:
            print(f"    ({s.lo}, {s.hi}): {line}")

    print()
    print("=" * 72)
    print("场景 4：预算耗尽 —— 未决边界明确保留，不作猜测")
    print("=" * 72)
    rep0 = check_continuity(k, budget=0)
    print(rep0.summary())

    print()
    print("=" * 72)
    print("场景 5：片段编辑 —— 增量维护共同细分与来源映射")
    print("=" * 72)
    print("编辑前边界：", [str(b) for b in f.boundaries])
    f.replace_piece(2, Interval.closed(1, 4), rf((0, 1), (-2, 1)), "f2': x/(x-2)")
    print("把 f2 改为 [1,4] 上的 x/(x-2)（在 x=2 有极点）")
    print("编辑后边界：", [str(b) for b in f.boundaries])
    show_model(f)
    print()
    print(check_continuity(f).summary())
    print()
    print("编辑日志：")
    for line in f.edits:
        print("  " + line)


if __name__ == "__main__":
    main()
