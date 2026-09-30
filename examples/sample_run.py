"""运行样例（合成数据）：层级泛化 + 抑制预算下的最低损失 k-匿名方案。

运行：python3 examples/sample_run.py
"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from kanonym import Anonymizer, Hierarchy, Record, below_k_records, group_records

# ---- 合成数据的泛化层级（child -> parent，根的父指向自身） ----
AGE = Hierarchy("age", {
    "23": "20-29", "25": "20-29", "28": "20-29",
    "31": "30-39", "34": "30-39", "38": "30-39",
    "45": "40-49", "47": "40-49",
    "20-29": "成年", "30-39": "成年", "40-49": "中年",
    "成年": "*", "中年": "*", "*": "*",
})
CITY = Hierarchy("city", {
    "北京": "华北", "天津": "华北",
    "上海": "华东", "南京": "华东",
    "广州": "华南", "深圳": "华南",
    "华北": "中国", "华东": "中国", "华南": "中国",
    "中国": "*", "*": "*",
})
JOB = Hierarchy("job", {
    "工程师": "技术", "分析师": "技术",
    "教师": "教育", "医生": "医疗", "护士": "医疗",
    "技术": "*", "教育": "*", "医疗": "*", "*": "*",
})
HIERARCHIES = [AGE, CITY, JOB]

# ---- 合成记录：注意 s2 有两条记录，r11 的城市为空值 ----
RECORDS = [
    Record("r1", "s1", ("23", "北京", "工程师")),
    Record("r2", "s2", ("25", "北京", "工程师")),
    Record("r3", "s2", ("25", "天津", "分析师")),   # 同主体第二条
    Record("r4", "s3", ("28", "天津", "教师")),
    Record("r5", "s4", ("31", "上海", "医生")),
    Record("r6", "s5", ("34", "上海", "护士")),
    Record("r7", "s6", ("38", "南京", "教师")),
    Record("r8", "s7", ("45", "广州", "工程师")),
    Record("r9", "s8", ("47", "深圳", "分析师")),
    Record("r10", "s9", ("31", "广州", "医生")),
    Record("r11", "s10", ("38", None, "护士")),     # 空值：单独类别
]

K = 3
SUP_BUDGET = 2


def show(title, value):
    print(f"  {title}: {value}")


def main():
    print("=" * 68)
    print("合成数据 k-匿名试算（仅评价声明指标，不构成形式化隐私证明）")
    print("=" * 68)

    print("\n[1] 原始数据（不泛化）的等价类与不足 k 记录")
    zero = (0, 0, 0)
    classes = group_records(RECORDS, HIERARCHIES, zero)
    for key, recs in sorted(classes.items()):
        subs = sorted({r.subject for r in recs})
        print(f"  {key}  记录={len(recs)} 主体数={len(subs)} {subs}")
    bad = below_k_records(RECORDS, HIERARCHIES, zero, K)
    show(f"不足 k={K} 的记录", [r.rid for r in bad])

    print("\n[2] 联合泛化层级 + 抑制预算求最低损失方案")
    anon = Anonymizer(HIERARCHIES, k=K, sup_budget=SUP_BUDGET, metric="ncp")
    result = anon.load(RECORDS)
    best = result.best
    show("状态", result.status)
    show("泛化向量 (age, city, job)", best.vector)
    print(f"  枚举评估: {result.n_evaluated}/{result.n_total} 个向量")
    show("总信息损失 (NCP + 抑制)", f"{best.loss:.4f}")
    show("抑制记录", list(best.suppressed))
    print("  等价类（泛化后重新分组、重新计数）:")
    for key, recs in sorted(best.classes.items()):
        subs = sorted({r.subject for r in recs})
        ok = "达标" if len(subs) >= K else "不足k->抑制"
        print(f"    {key}  记录={len(recs)} 主体数={len(subs)} [{ok}]")

    print("\n[3] 每记录归属证据（节选）")
    for rid in ("r1", "r3", "r11"):
        e = anon.evidence[rid]
        print(f"  {rid}: 类={e['class_key']} 主体数={e['class_subject_count']} "
              f"满足k={e['meets_k']} 被抑制={e['suppressed']}")

    print("\n[4] 预算中断的上下界（max_evaluations=10）")
    from kanonym import solve_exact
    partial = solve_exact(RECORDS, HIERARCHIES, K, SUP_BUDGET,
                          metric="ncp", max_evaluations=10)
    show("状态", partial.status)
    show("最优损失下界", f"{partial.lower_bound:.4f}")
    show("最优损失上界", f"{partial.upper_bound:.4f}" if partial.upper_bound else "未知")
    print("  注: 本例低损失向量均因抑制预算不足而不可行, 上界须在评估到")
    print("      高泛化向量后才出现; 下界始终有效并随评估推进收紧。")

    print("\n[5] 增量修复：撤回 r5、r6，再新增一条记录")
    report = anon.remove_records(["r5", "r6"])
    show("旧向量仍可行", report["old_vector_still_feasible"])
    show("新向量", report["new_vector"])
    show("新损失", f"{report['new_loss']:.4f}")
    show("全部组重新校验", report["all_groups_revalidated"])
    report = anon.add_records([Record("r12", "s11", ("34", "上海", "医生"))])
    show("新增 r12 后向量", report["new_vector"])
    show("新增 r12 后损失", f"{report['new_loss']:.4f}")
    show("各组主体计数", report["group_subject_counts"])

    print("\n说明：以上为合成数据上的指标试算，不宣称绝对匿名，"
          "统计实验不作为形式隐私证明。")


if __name__ == "__main__":
    main()
