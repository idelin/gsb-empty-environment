"""运行样例：合成数据上的 k-匿名联合优化、界中断、增量修复与精确核验。

仅使用合成数据。输出的信息损失仅为声明指标（NCP + 抑制惩罚），
不构成绝对匿名或完整隐私保障的声明。
"""
from __future__ import annotations

from kanonymity import (
    Hierarchy,
    IncrementalAnonymizer,
    Record,
    check_optimal,
    compute_classes,
    optimize,
)

# ---------- 合成数据的泛化层级 ----------
AGE = Hierarchy("age", {
    "*": None,
    "young": "*", "mid": "*", "senior": "*",
    "18-24": "young", "25-34": "young",
    "35-44": "mid", "45-54": "mid",
    "55-64": "senior", "65+": "senior",
})
CITY = Hierarchy("city", {
    "*": None,
    "north": "*", "south": "*",
    "beijing": "north", "tianjin": "north",
    "shanghai": "south", "shenzhen": "south",
})
JOB = Hierarchy("job", {
    "*": None,
    "tech": "*", "health": "*",
    "engineer": "tech", "designer": "tech",
    "doctor": "health", "nurse": "health",
})
HIERARCHIES = (AGE, CITY, JOB)

K = 2
BUDGET = 3          # 抑制预算（最多移除的记录数）
COUNT_MODE = "record"  # 按记录计数；可改为 "subject" 按主体计数


def synthetic_records() -> list[Record]:
    rows = [
        ("r1",  "s1", "18-24", "beijing",  "engineer"),
        ("r2",  "s2", "25-34", "beijing",  "engineer"),
        ("r3",  "s3", "18-24", "tianjin",  "designer"),
        ("r4",  "s4", "35-44", "shanghai", "doctor"),
        ("r5",  "s5", "45-54", "shanghai", "doctor"),
        ("r6",  "s6", "35-44", "shenzhen", "nurse"),
        ("r7",  "s7", "55-64", "shenzhen", "nurse"),
        ("r8",  "s8", "65+",   "beijing",  "engineer"),
        ("r9",  "s9", None,    "tianjin",  "designer"),   # 空值：独立类别
        ("r10", "s10", "25-34", "tianjin", "designer"),
    ]
    return [Record(rid, sub, (a, c, j)) for rid, sub, a, c, j in rows]


def show_classes(records: list[Record], levels: tuple[int, ...]) -> None:
    classes = compute_classes(records, levels, HIERARCHIES, COUNT_MODE)
    print(f"\n[等价类] 层向量={levels}，共 {len(classes)} 类（k={K}）")
    for key, ec in sorted(classes.items()):
        flag = "OK " if ec.count >= K else "不足k"
        print(f"  {flag} key={key} 计数={ec.count} 记录={list(ec.member_rids)}")


def show_solution(tag: str, result) -> None:
    print(f"\n[{tag}] optimal={result.optimal} "
          f"评估 {result.evaluated}/{result.total_vectors} 个层向量")
    print(f"  下界={result.lower_bound:.4f}  上界={result.upper_bound:.4f}")
    if result.best is None:
        print("  无可行解（预算内无法满足 k）")
        return
    s = result.best
    print(f"  最优层向量={s.levels} 总损失={s.loss:.4f} "
          f"(泛化={s.gen_loss:.4f}, 抑制={s.suppression_loss:.4f})")
    print(f"  抑制记录={sorted(s.suppressed_rids)}")
    print("  每记录归属证据：")
    for ev in s.evidence:
        print(f"    {ev.rid}(主体{ev.subject}) -> {ev.action} "
              f"类={ev.class_key} 类计数={ev.class_count} 满足k={ev.k_satisfied}")


def main() -> None:
    records = synthetic_records()
    print("=== 合成数据 k-匿名试算（非隐私证明） ===")
    print(f"参数: k={K}, 抑制预算={BUDGET}, 计数方式={COUNT_MODE}, "
          f"记录数={len(records)}")

    show_classes(records, (0, 0, 0))

    # 1) 联合优化：泛化层级 + 抑制预算下的最低损失方案
    result = optimize(records, HIERARCHIES, K, BUDGET, count_mode=COUNT_MODE)
    show_solution("联合优化", result)

    # 2) 小域精确枚举核验最优性
    ok = check_optimal(result, records, HIERARCHIES, K, BUDGET,
                       count_mode=COUNT_MODE)
    print(f"\n[精确核验] 暴力枚举核验最优性: {'通过' if ok else '失败'}")

    # 3) 预算中断：限制评估次数，给出上下界
    limited = optimize(records, HIERARCHIES, K, BUDGET, count_mode=COUNT_MODE,
                       max_evals=8)
    show_solution("预算中断(max_evals=8)", limited)
    print(f"  界一致性核验: "
          f"{'通过' if check_optimal(limited, records, HIERARCHIES, K, BUDGET, count_mode=COUNT_MODE) else '失败'}")

    # 4) 增量修复：撤回一条记录后全局重查（不能只看局部组）
    inc = IncrementalAnonymizer(HIERARCHIES, K, BUDGET, count_mode=COUNT_MODE)
    inc.load(records)
    print("\n[增量修复] 撤回 r2 后全局重新优化：")
    after = inc.remove("r2")
    show_solution("撤回 r2 后", after)
    print("\n[增量修复] 新增 r11(25-34, beijing, engineer) 后：")
    after_add = inc.add(Record("r11", "s11", ("25-34", "beijing", "engineer")))
    show_solution("新增 r11 后", after_add)


if __name__ == "__main__":
    main()
