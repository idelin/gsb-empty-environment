"""运行样例（全部为合成数据）：演示极大可行时间段求解、不一致原因、
观察时刻可见性、预算截断保留未知段、增量撤销受影响段、证据重放。"""
from evidence import (
    EvidenceStore,
    FactVersion,
    IncrementalSolver,
    MutexRuleVersion,
    replay,
    solve_store,
)


def show(result, title):
    print(f"\n=== {title} ===")
    print(f"观察时刻 T={result.observation_time}  digest={result.digest()[:16]}...")
    print(f"证据: facts={list(result.evidence.fact_refs)} rules={list(result.evidence.rule_refs)}")
    if not result.segments:
        print("  (无可行时间段)")
    for s in result.segments:
        print(f"  {s.segment_id} [{s.status}] [{s.start}, {s.end})")
        for p in s.cover:
            print(f"      覆盖 [{p.start}, {p.end}) 选择={list(p.selection.fact_refs)}")
    for r in result.reasons:
        print(f"  原因 {r.reason_id} [{r.kind}] {r.detail}")


def scenario_basic():
    """多来源 + 互斥：sensorB 与 manual 互斥；晚录入的事实不可见。"""
    store = EvidenceStore()
    store.add_fact_version(FactVersion("F1", 1, "sensorA", recorded_at=1, valid_from=0, valid_to=5))
    store.add_fact_version(FactVersion("F1", 2, "sensorA", recorded_at=4, valid_from=0, valid_to=6))
    store.add_fact_version(FactVersion("F2", 1, "sensorB", recorded_at=2, valid_from=2, valid_to=9))
    store.add_fact_version(FactVersion("F3", 1, "manual",  recorded_at=3, valid_from=1, valid_to=7))
    # 观察时刻之后录入，不得参与历史解释
    store.add_fact_version(FactVersion("F4", 1, "sensorA", recorded_at=10, valid_from=0, valid_to=12))
    store.add_rule_version(MutexRuleVersion("R1", 1, frozenset({"sensorB", "manual"}), recorded_at=0))

    result = solve_store(store, observation=6)
    show(result, "场景1: 多版本 + 来源互斥 + 可见性 (T=6)")
    assert "F4@v1" not in result.evidence.fact_refs
    assert "F1@v2" in result.evidence.fact_refs and "F1@v1" not in result.evidence.fact_refs

    limited = solve_store(store, observation=6, budget=1)
    show(limited, "场景1b: 团枚举预算=1（截断，保留未知段）")
    assert limited.truncated
    assert any(s.status == "unknown" for s in limited.segments)
    return store, result


def scenario_no_common_interval():
    """[0,5) 与 [5,9) 半开区间无共同区间。"""
    store = EvidenceStore()
    store.add_fact_version(FactVersion("G1", 1, "sensorA", recorded_at=1, valid_from=0, valid_to=5))
    store.add_fact_version(FactVersion("G2", 1, "sensorA", recorded_at=1, valid_from=5, valid_to=9))
    result = solve_store(store, observation=2)
    show(result, "场景2: [0,5) 与 [5,9) 无共同区间")
    assert not result.feasible_segments()
    assert any(r.kind == "EMPTY_COMMON_INTERVAL" for r in result.reasons)


def scenario_incremental(store):
    inc = IncrementalSolver(store, observation=6)
    before = inc.result.digest()
    report = inc.update_fact(
        FactVersion("F2", 2, "sensorB", recorded_at=5, valid_from=3, valid_to=9))
    print("\n=== 场景3: 修改 F2 -> v2 [3,9)，增量撤销受影响段 ===")
    print(f"  受影响 span={list(report.span)}  full_refresh={report.full_refresh}")
    print(f"  撤销段: {list(report.revoked)}")
    print(f"  保留段: {list(report.kept)}")
    print(f"  新增段: {list(report.added)}")
    fresh = solve_store(store, observation=6)
    assert inc.result.digest() == fresh.digest(), "增量结果必须与全量重算一致"
    assert before != inc.result.digest()
    show(inc.result, "场景3: 增量更新后的结果")


def scenario_replay(store):
    expected = solve_store(store, observation=6)
    replayed = replay(store.events)
    again = solve_store(replayed, observation=6)
    print("\n=== 场景4: 事件日志重放 ===")
    print(f"  事件数={len(store.events)}  当前 digest={expected.digest()[:16]}...")
    print(f"  重放 digest={again.digest()[:16]}...  一致={again.digest() == expected.digest()}")
    assert again.digest() == expected.digest()


def main():
    store, _ = scenario_basic()
    scenario_no_common_interval()
    scenario_incremental(store)
    scenario_replay(store)
    print("\n全部样例断言通过。")


if __name__ == "__main__":
    main()
