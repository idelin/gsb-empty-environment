"""运行样例：一致性水位快照 + 变更缓存回放 + 失败不发布混合状态。"""

from cdc import OP_DELETE, OP_INSERT, OP_UPDATE, FixtureSource, Replicator, TableStore


def show(title, state):
    print(f"\n== {title} ==")
    for table, rows in sorted(state.items()):
        for pk, row in sorted(rows.items()):
            print(f"  {table}[{pk}] = {row}")


def main():
    src = FixtureSource()
    # 水位前：3 行插入 + 1 行更新（更新会进入快照，不得重复应用）
    src.apply_change("users", 1, OP_INSERT, after={"name": "alice", "balance": 100})
    src.apply_change("users", 2, OP_INSERT, after={"name": "bob", "balance": 50})
    src.apply_change("users", 3, OP_INSERT, after={"name": "carol", "balance": 30})
    src.apply_change("users", 1, OP_UPDATE,
                     before={"name": "alice", "balance": 100},
                     after={"name": "alice", "balance": 120})

    # 快照扫描期间的并发变更
    def during_scan(scanned):
        if scanned == 1:
            print("  [扫描期间] 更新 users[1]: balance 120 -> 200")
            src.apply_change("users", 1, OP_UPDATE,
                             before={"name": "alice", "balance": 120},
                             after={"name": "alice", "balance": 200})
        elif scanned == 2:
            print("  [扫描期间] 删除 users[2] (bob)")
            src.apply_change("users", 2, OP_DELETE,
                             before={"name": "bob", "balance": 50})
        elif scanned == 3:
            print("  [扫描期间] 插入 users[4] (dave)")
            src.apply_change("users", 4, OP_INSERT,
                             after={"name": "dave", "balance": 10})

    target = TableStore()
    rep = Replicator(src, target, overlap=2)  # 重叠拉取，验证水位过滤
    print("-- 第一轮：快照 + 水位后回放 --")
    watermark = rep.run(during_scan=during_scan)
    print(f"一致性水位 commit_seq = {watermark}")
    s = rep.stats
    print(f"缓存 {s.cached} 笔 | 应用 {s.applied} 笔 | "
          f"水位前跳过 {s.skipped_before_watermark} 笔 | "
          f"重复事务跳过 {s.skipped_duplicate_txn} 笔")
    show("目标库最终状态（与源库一致）", target.state())
    assert target.state() == src.live_state(), "目标库与源库不一致！"

    # 快照失败：目标库必须保持原状
    print("\n-- 第二轮：快照失败，不发布混合状态 --")
    src.apply_change("users", 5, OP_INSERT, after={"name": "erin", "balance": 5})
    src.fail_snapshot = True
    before = target.state()
    result = rep.run(during_scan=during_scan)
    print(f"run() 返回 {result}（None = 快照失败，未发布）")
    print(f"目标库保持原状: {target.state() == before}, "
          f"users[5] 未泄漏: {5 not in target.state().get('users', {})}")


if __name__ == "__main__":
    main()
