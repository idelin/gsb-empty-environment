"""运行样例：合成偏移转换表 + 三个小场景。

用法: python3.11 demo.py
"""

from __future__ import annotations

from fractions import Fraction

from clockalign import (
    ExtrapolationError,
    JunctionSideError,
    WallTime,
    correct,
    make_anchor,
    reconcile,
)
from clockalign.model import make_model
from clockalign.synthetic import make_synthetic_table


def hr(title: str) -> None:
    print()
    print("=" * 72)
    print(title)
    print("=" * 72)


def show_queries(recon, table, points, margin):
    for p in points:
        try:
            c = correct(recon, WallTime(p))
        except ExtrapolationError as exc:
            print(f"  T+{p}s -> 拒绝: {exc}")
            continue
        truth = table.true_reference_at(Fraction(p))
        inside = c.lower.seconds <= truth <= c.upper.seconds
        print(
            f"  T+{p}s -> [{c.lower.approx()}, {c.upper.approx()}] "
            f"mid={c.mid.approx()} ±{c.uncertainty.approx()} "
            f"真值={float(truth):.6f} 覆盖真值={inside}"
        )


def demo_synthetic() -> None:
    hr("样例 1: 合成偏移转换表（3 段连续，14 锚点 + 2 异常）")
    table = make_synthetic_table()
    print(f"锚点 {len(table.anchors)} 个，误差区间 ±{float(Fraction(1, 20))}s，"
          f"最大漂移斜率 ±0.001，外推允许距离 {table.extrapolation_margin}s")

    recon = reconcile(
        table.model,
        table.anchors,
        extrapolation_margin=table.extrapolation_margin,
    )
    print(f"状态: {recon.status}, 最少删除数: {recon.min_removed}")
    print(f"最优性证据: {recon.optimality_proof}")
    print("冲突核（锚点下标）:")
    for core in recon.conflict_cores:
        names = [table.anchors[i].label() for i in core]
        print(f"  {core} -> {names}")
    print(f"最优方案数: {len(recon.scenarios)}")
    for s in recon.scenarios:
        removed = [table.anchors[i].label() for i in sorted(s.removed)]
        print(f"  删除 {sorted(s.removed)} -> {removed}")
        for seg, (alpha, beta) in enumerate(s.representative):
            print(f"    段{seg}: alpha={alpha} (~{float(alpha):.6f}), "
                  f"beta={beta} (~{float(beta):.4f})")

    print("查询（内插与交界附近）:")
    show_queries(recon, table, [0, 300, 600, 999, 1000, 1001, 2000, 2500, 2999, 3000, 3600, 4000],
                 table.extrapolation_margin)
    print("查询（外推）:")
    show_queries(recon, table, [-200, -201, 4200, 4201], table.extrapolation_margin)


def demo_budget_undecided() -> None:
    hr("样例 2: 预算未决（预算 1 < 最少删除数 2，与最优结果分开报告）")
    table = make_synthetic_table()
    recon = reconcile(table.model, table.anchors, budget=1,
                      extrapolation_margin=table.extrapolation_margin)
    print(f"状态: {recon.status}")
    print(f"说明: {recon.optimality_proof}")
    print(f"搜索轨迹（不可行叶）: 共 {len(recon.trace)} 条，示例:")
    for line in recon.trace[:4]:
        print(f"  {line}")


def demo_duplicate_local() -> None:
    hr("样例 3: 同一本地时间、不同参考值 -> 冲突核指认")
    anchors = [
        make_anchor(0, 100, "0.1", name="P0"),
        make_anchor(10, 110, "0.1", name="P1"),
        make_anchor(10, 115, "0.1", name="P1'"),  # 与 P1 同本地时间但区间不相交
        make_anchor(20, 120, "0.1", name="P2"),
    ]
    model = make_model([], rate=(Fraction(1, 2), Fraction(3, 2)))
    recon = reconcile(model, anchors)
    print(f"最少删除数: {recon.min_removed}")
    print("冲突核:")
    for core in recon.conflict_cores:
        print(f"  {[anchors[i].label() for i in core]}")
    for s in recon.scenarios:
        print(f"  方案: 删除 {[anchors[i].label() for i in sorted(s.removed)]}")
    c = correct(recon, WallTime(10))
    print(f"  查询: {c.describe()}")


def demo_declared_jump() -> None:
    hr("样例 4: 声明不连续交界（跳变 <= 0.5s）与交界点查询")
    anchors = [
        make_anchor(0, 0, "0.1", name="J0"),
        make_anchor(10, 10, "0.1", name="J1"),
        make_anchor(20, "20.3", "0.1", name="J2"),   # 交界后参考钟跳变 +0.3s
        make_anchor(30, "30.3", "0.1", name="J3"),
    ]
    model_jump = make_model([15], rate=(1, 1), continuous=False, max_jump="0.5")
    recon = reconcile(model_jump, anchors, extrapolation_margin=5)
    print(f"声明跳变模型: 状态={recon.status}, 最少删除数={recon.min_removed}")
    try:
        correct(recon, WallTime(15))
    except JunctionSideError as exc:
        print(f"  交界点查询未指明侧: 拒绝 -> {exc}")
    for side in ("left", "right"):
        c = correct(recon, WallTime(15), side=side)
        print(f"  T+15s (side={side}) -> {c.describe()}")

    model_cont = make_model([15], rate=(1, 1), continuous=True)
    recon_c = reconcile(model_cont, anchors)
    print(f"强制连续模型: 状态={recon_c.status}, 最少删除数={recon_c.min_removed}")
    print(f"  最优性证据: {recon_c.optimality_proof}")


def demo_enum_cross_check() -> None:
    hr("样例 5: 有理约束枚举对照（穷举顶点 vs 单纯形 LP）")
    anchors = [
        make_anchor(0, 100, "0.5", name="E0"),
        make_anchor(5, 105, "0.5", name="E1"),
        make_anchor(15, 115, "0.5", name="E2"),
        make_anchor(20, 120, "0.5", name="E3"),
    ]
    model = make_model([10], rate=(Fraction(9, 10), Fraction(11, 10)), continuous=True)
    recon = reconcile(model, anchors, cross_check=True)
    print(f"最少删除数: {recon.min_removed}（每个查询点都与顶点枚举对照）")
    for p in (0, 3, 10, 17, 20):
        c = correct(recon, WallTime(p))
        print(f"  T+{p}s -> {c.describe()}  [枚举对照通过]")


def main() -> None:
    demo_synthetic()
    demo_budget_undecided()
    demo_duplicate_local()
    demo_declared_jump()
    demo_enum_cross_check()


if __name__ == "__main__":
    main()
