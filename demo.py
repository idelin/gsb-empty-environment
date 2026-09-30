"""运行样例：逐步保守推导 + 小有限域邻接枚举核验（仅合成数据）。

运行：python3.11 demo.py
"""

from finite_check import check_on_finite_domain, format_dataset
from sensitivity import (
    Add,
    Clip,
    Count,
    GroupSum,
    Join,
    Sum,
    Table,
    UnboundedContributionError,
    analyze,
)

SUBJECTS = ("u1", "u2")
GRID01 = (0.0, 1.0)


def show(title, query, tables=None, grids=None):
    print("=" * 72)
    print(title)
    print("-" * 72)
    bound, deriv = analyze(query)
    print(deriv.render())
    print(f"敏感度上界 = {bound:g}")
    if tables is not None:
        res = check_on_finite_domain(query, tables, SUBJECTS, grids)
        print("有限域核验:", res.summary())
        if res.witness is not None:
            d1, d2, subject = res.witness
            print(f"达到最大观测值的邻接见证（替换主体 {subject}）:")
            print("  D  =", format_dataset(d1, tables, SUBJECTS))
            print("  D' =", format_dataset(d2, tables, SUBJECTS))
    print()


def main():
    print("主体级 L1 敏感度保守推导演示（仅合成数据；枚举核验非形式化证明）\n")

    t = Table("T", max_rows_per_subject=3, value_lo=0.0, value_hi=2.0)
    show("1. sum：同主体最多 m=3 行、每行 [0,2] => 界 m*b = 6", Sum(t))

    t2 = Table("T", max_rows_per_subject=2, value_lo=0.0, value_hi=1.0)
    show("2. count：界 = m = 2（枚举域上达到）",
         Count(t2), [t2], {"T": GRID01})

    wide = Table("W", max_rows_per_subject=2, value_lo=0.0, value_hi=100.0)
    show("3. 裁剪：值域 [0,100] 裁到 [0,2] => 界 2*2 = 4",
         Sum(Clip(wide, 0.0, 2.0)), [wide], {"W": (0.0, 100.0)})

    g = Table("G", max_rows_per_subject=2, value_lo=0.0, value_hi=1.0,
              groups=("a", "b"))
    show("4. 固定分组 sum：替换邻接下旧/新行可占不同组 => 界 2*m*b = 4",
         GroupSum(g), [g], {"G": GRID01})

    left = Table("L", max_rows_per_subject=2, value_lo=0.0, value_hi=1.0)
    right = Table("R", max_rows_per_subject=3, value_lo=0.0, value_hi=2.0)
    show("5. 连接：贡献上限联合传播 m_L*m_R*(b_L*b_R) = 2*3*2 = 12",
         Sum(Join(left, right)))

    j1 = Table("J1", max_rows_per_subject=1, value_lo=0.0, value_hi=1.0)
    j2 = Table("J2", max_rows_per_subject=1, value_lo=0.0, value_hi=1.0)
    show("6. 连接的小有限域核验：m=1,b=1 两侧 => 界 1，枚举达到",
         Sum(Join(j1, j2)), [j1, j2], {"J1": GRID01, "J2": GRID01})

    s1 = Table("S1", max_rows_per_subject=1, value_lo=0.0, value_hi=1.0)
    s2 = Table("S2", max_rows_per_subject=1, value_lo=0.0, value_hi=1.0)
    show("7. 组合（同一主体同时出现在两表）：界相加 1+1=2，非取 max",
         Add(Sum(s1), Sum(s2)), [s1, s2], {"S1": GRID01, "S2": GRID01})

    same = Table("S", max_rows_per_subject=1, value_lo=0.0, value_hi=1.0)
    show("8. 同一表被引用两次：主体贡献关联保留 => 界 2",
         Add(Sum(same), Sum(same)), [same], {"S": GRID01})

    print("=" * 72)
    print("9. 无界贡献：拒绝")
    print("-" * 72)
    unbounded = Table("U", max_rows_per_subject=None)
    try:
        analyze(Sum(unbounded))
    except UnboundedContributionError as exc:
        print("拒绝：", exc)
        print("部分推导树：")
        print(exc.derivation.render())
    try:
        analyze(Sum(Join(Table("B", max_rows_per_subject=1), unbounded)))
    except UnboundedContributionError as exc:
        print("拒绝（连接一侧无界）：", exc)
    print()
    print("说明：以上枚举仅覆盖有限合成域，是工程一致性核验与紧性见证，")
    print("不构成形式化隐私证明；统计实验不能替代证明。")


if __name__ == "__main__":
    main()
