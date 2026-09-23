"""运行样例: 确定性流生成 -> oracle 差分 -> 失败自动缩减 -> 错误分类。

用法: python3 demo.py [seed]
"""
import sys

from cdc import (
    TABLES,
    ArrivalOrderProcessor,
    IncrementalProcessor,
    freeze_row,
    generate,
    run_case,
    shrink,
    thaw_row,
    validate_stream,
)
from cdc.events import Begin, Change, Commit


def show_state(title, state):
    rows = sum(len(r) for r in state.values())
    print(f"    {title}: {rows} 行")
    for table in sorted(state):
        for pk in sorted(state[table]):
            print(f"      {table}/{pk} = {thaw_row(state[table][pk])}")


def main():
    seed = int(sys.argv[1]) if len(sys.argv) > 1 else 0

    print("=" * 64)
    print("[1] 生成确定性测试流 (seed=%d)" % seed)
    g = generate(seed=seed)
    s = g.stats
    print(f"    事务 {s['txns']} 个, 事件 {s['events']} 条, "
          f"重复 {s['duplicates']} 条, 乱序事务 {s['reordered_txns']} 个, "
          f"崩溃点 {s['crashes']} 个")
    validate_stream(g.stream, TABLES)
    print("    流校验: 通过 (结构有效)")

    print("=" * 64)
    print("[2] 被测处理器 vs 独立 oracle (最终全量状态)")
    result = run_case(g.stream, TABLES, IncrementalProcessor)
    print(f"    结果: {result.status}")
    show_state("最终状态", result.actual)

    print("=" * 64)
    print("[3] 注入缺陷实现 (按到达顺序应用, 忽略 seq 乱序)")
    buggy = run_case(g.stream, TABLES, ArrivalOrderProcessor)
    print(f"    结果: {buggy.status}")
    for table, pk, expected, actual in buggy.diffs:
        print(f"    差异 {table}/{pk}: 期望 {thaw_row(expected)}, "
              f"实际 {thaw_row(actual)}")
    if buggy.status != "mismatch":
        print("    该 seed 未触发缺陷(乱序未命中同主键变更), "
              "请换 seed, 例如: python3 demo.py 0")
        return

    print("=" * 64)
    print("[4] 自动缩减失败序列 (保持事务结构可重放)")
    shrunk = shrink(g.stream, TABLES, ArrivalOrderProcessor)
    print(f"    {len(g.stream)} 条事件 -> {len(shrunk)} 条事件")
    validate_stream(shrunk, TABLES)
    print("    缩减后流校验: 通过 (事务结构完整, 可重放)")
    for ev in shrunk:
        print(f"      {ev}")
    recheck = run_case(shrunk, TABLES, ArrivalOrderProcessor)
    print(f"    缩减后复测: {recheck.status} (仍复现失败)")

    print("=" * 64)
    print("[5] 无效事件流与处理错误分开归类")
    invalid = [Begin(0), Change(0, 0, "不存在的表", "x", None, None), Commit(0, 0)]
    r1 = run_case(invalid, TABLES)
    print(f"    未知表事件        -> {r1.status}: {r1.error}")
    conflict = [
        Begin(0),
        Change(0, 0, "users", "u1", None, freeze_row({"balance": 1})),
        Change(0, 0, "users", "u1", None, freeze_row({"balance": 2})),
        Commit(0, 0),
    ]
    r2 = run_case(conflict, TABLES)
    print(f"    同 seq 内容冲突   -> {r2.status}: {r2.error}")


if __name__ == "__main__":
    main()
