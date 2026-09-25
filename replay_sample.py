"""可重放样例:按固定脚本演示掩码矩阵乘的全部事务行为。

运行: python3 replay_sample.py
输出确定,可反复重放;每个样例内部都与稠密参考或手工期望值核对。
"""

from masked_matmul import (
    ComplementMask,
    Engine,
    ExplicitMask,
    SpillError,
    UnknownVersionError,
    dense_reference,
)


def run():
    lines = []
    emit = lines.append

    emit("== 样例 1: 两条 k 路径抵消为 0, 撤回其一恢复非零 ==")
    eng = Engine(2, 2, 2, ExplicitMask(2, 2), row_threshold=4)
    with eng.begin() as t:
        t.m_insert(0, 0)
        t.a_insert(0, 0, 1)
        t.b_insert(0, 0, 5)
        t.a_insert(0, 1, 1)
        t.b_insert(1, 0, -5)
    emit(f"抵消后 {eng.latest}: {eng.query_latest()} (空结果, 两条路径 +5/-5 抵消)")
    assert eng.query_latest() == ()
    assert eng.index.stats()["total_source_entries"] == 2
    with eng.begin() as t:
        t.a_delete(0, 1, 1)
    emit(f"撤回 A(0,1) 后 {eng.latest}: {eng.query_latest()}")
    assert eng.query_latest() == ((0, 0, 5),)

    emit("== 样例 2: 掩码删除只影响可见性, 重新加入恢复贡献 ==")
    with eng.begin() as t:
        t.m_delete(0, 0)
    emit(f"删除掩码 {eng.latest}: {eng.query_latest()}")
    assert eng.query_latest() == ()
    with eng.begin() as t:
        t.a_insert(0, 0, 2)
    emit(f"隐藏期间继续累加 {eng.latest}: {eng.query_latest()} (仍不可见)")
    with eng.begin() as t:
        t.m_insert(0, 0)
    emit(f"恢复掩码 {eng.latest}: {eng.query_latest()} (1+2)*5=15 完整恢复")
    assert eng.query_latest() == ((0, 0, 15),)

    emit("== 样例 3: 事务取消保持已发布结果 ==")
    before = eng.latest
    txn = eng.begin()
    txn.a_insert(1, 0, 9)
    txn.b_insert(0, 1, 9)
    txn.abort()
    emit(f"取消后版本不变 {eng.latest == before}: {eng.query_latest()}")
    assert eng.latest == before and eng.query_latest() == ((0, 0, 15),)

    emit("== 样例 4: 热点行溢写, 溢写失败回滚且已发布结果不变 ==")
    hot = Engine(1, 8, 1, ComplementMask(1, 1), row_threshold=3)
    with hot.begin() as t:
        for k in range(4):
            t.b_insert(k, 0, 1)
            t.a_insert(0, k, 1)
    stats = hot.index.stats()
    emit(f"热点行 {hot.latest}: {hot.query_latest()} 溢写行={stats['spilled_rows']}")
    assert hot.query_latest() == ((0, 0, 4),) and stats["spilled_rows"] == 1
    published_vec = hot.latest
    hot.spill_store.fail_writes = True
    txn = hot.begin()
    try:
        for k in range(4, 8):
            txn.b_insert(k, 0, 1)
            txn.a_insert(0, k, 1)
        txn.commit()
        raise AssertionError("应当发生溢写失败")
    except SpillError:
        emit("溢写失败: 事务已自动回滚")
    hot.spill_store.fail_writes = False
    emit(f"已发布结果保持 {hot.latest == published_vec}: {hot.query_latest()}")
    assert hot.latest == published_vec and hot.query_latest() == ((0, 0, 4),)
    with hot.begin() as t:
        for k in range(4, 8):
            t.b_insert(k, 0, 1)
            t.a_insert(0, k, 1)
    emit(f"重试成功 {hot.latest}: {hot.query_latest()}")
    assert hot.query_latest() == ((0, 0, 8),)

    emit("== 样例 5: 补集掩码, 10^6 维不物化稠密矩阵 ==")
    big = Engine(10**6, 10**6, 10**6, ComplementMask(10**6, 10**6))
    with big.begin() as t:
        t.a_insert(123, 456, 2)
        t.b_insert(456, 789, 3)
    emit(f"结果 {big.latest}: {big.query_latest()} 掩码存储项={big.mask.stored_entries()}")
    assert big.query_latest() == ((123, 789, 6),)
    with big.begin() as t:
        t.m_delete(123, 789)
    emit(f"排除后 {big.latest}: {big.query_latest()} 掩码存储项={big.mask.stored_entries()}")
    assert big.query_latest() == () and big.mask.stored_entries() == 1
    with big.begin() as t:
        t.m_insert(123, 789)
    emit(f"恢复后 {big.latest}: {big.query_latest()}")
    assert big.query_latest() == ((123, 789, 6),)

    emit("== 样例 6: 混合更新对照自写稠密参考 ==")
    ref_eng = Engine(3, 3, 3, ExplicitMask(3, 3), row_threshold=2)
    script = [
        ("m", "ins", 0, 0), ("m", "ins", 0, 2), ("m", "ins", 1, 1),
        ("m", "ins", 2, 0), ("m", "ins", 2, 2),
        ("a", "ins", 0, 1, 2), ("a", "ins", 1, 0, 1),
        ("a", "ins", 2, 2, 3), ("a", "ins", 0, 2, 1),
        ("b", "ins", 1, 0, 4), ("b", "ins", 0, 1, 5),
        ("b", "ins", 2, 2, 2), ("b", "ins", 1, 2, 1),
        ("b", "ins", 2, 0, 1),
        ("a", "del", 0, 2, 1), ("m", "del", 2, 0), ("b", "del", 2, 0, 1),
        ("a", "ins", 2, 0, -2), ("m", "ins", 2, 0),
    ]
    with ref_eng.begin() as t:
        for op in script:
            kind, action = op[0], op[1]
            if kind == "m":
                (t.m_insert if action == "ins" else t.m_delete)(op[2], op[3])
            elif kind == "a":
                (t.a_insert if action == "ins" else t.a_delete)(op[2], op[3], op[4])
            else:
                (t.b_insert if action == "ins" else t.b_delete)(op[2], op[3], op[4])
    expected = dense_reference(ref_eng.A, ref_eng.B, ref_eng.mask)
    actual = ref_eng.query_latest()
    emit(f"增量结果 {ref_eng.latest}: {actual}")
    emit(f"稠密参考        : {expected}")
    assert actual == expected, "增量结果与稠密参考不一致"
    ref_eng.index.check_against(ref_eng.A, ref_eng.B, ref_eng.mask)
    emit("对照一致, 贡献索引无孤儿")

    emit("== 样例 7: 查询绑定版本向量, 旧快照可并发读 ==")
    old_vec = ref_eng.latest
    old_snapshot = ref_eng.query(old_vec)
    with ref_eng.begin() as t:
        t.a_insert(1, 2, 7)
        t.b_insert(2, 1, 1)
    emit(f"新提交 {ref_eng.latest}: {ref_eng.query_latest()}")
    emit(f"旧版本 {old_vec} 仍可读: {ref_eng.query(old_vec)}")
    assert ref_eng.query(old_vec) == old_snapshot
    try:
        ref_eng.query((0, 99, 0))
        raise AssertionError("未提交的版本向量应被拒绝")
    except UnknownVersionError:
        emit("未提交过的版本向量被拒绝, 无法跨事务拼接矩阵")

    for engine in (eng, hot, big, ref_eng):
        engine.spill_store.cleanup()
    emit("OK")
    return "\n".join(lines)


def main():
    print(run())


if __name__ == "__main__":
    main()
