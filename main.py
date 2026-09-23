"""运行样例：本地夹具 -> 物化 -> 消费水位/快照 -> 墓碑回收（含阻塞原因）。"""
from cdc_gc import ConsumerGroup, GarbageCollector, MaterializedStore, MembershipError, load_fixture

FIXTURE = "cdc_gc/fixtures/changes.jsonl"
LATE_FIXTURE = "cdc_gc/fixtures/late_changes.jsonl"


def show_gc(gc, label):
    print(f"\n== {label} ==")
    decisions = gc.evaluate()
    if not decisions:
        print("  (无墓碑)")
    for decision in decisions:
        tomb = decision.tombstone
        if decision.reclaimable:
            print(f"  [可回收] {tomb.table}/{tomb.pk} 删除点={tomb.delete_seq}")
        else:
            print(f"  [阻塞]   {tomb.table}/{tomb.pk} 删除点={tomb.delete_seq}")
            for reason in decision.reasons:
                print(f"    - {reason}")


def main():
    store = MaterializedStore()
    group = ConsumerGroup(lambda: store.reclaimed_horizon)
    gc = GarbageCollector(store, group)

    print("== 1. 应用本地夹具变更 ==")
    for event in load_fixture(FIXTURE):
        result = store.apply(event)
        print(f"  seq={event.commit_seq} {event.op.value:<6} {event.table}/{event.pk} -> {result.outcome}")

    print("\n== 2. 显式成员变更：注册消费者并打开快照 ==")
    group.register("analytics", start_seq=0)
    group.register("audit", start_seq=0)
    group.register("experimental", start_seq=0)
    group.open_snapshot("snap-report", seq=3)
    print("  消费者: analytics@0, audit@0, experimental@0; 快照: snap-report@3")

    group.advance("analytics", 5)
    group.advance("audit", 2)
    print("  推进水位: analytics->5, audit->2 (audit 落后)")
    show_gc(gc, "3. 落后消费者与旧快照阻塞回收")

    group.deregister("experimental")
    print("\n  显式注销消费者 'experimental'")
    group.advance("audit", 5)
    print("  推进水位: audit->5")
    show_gc(gc, "4. 消费者均已越过删除点，快照仍阻塞")

    group.close_snapshot("snap-report")
    print("\n  关闭快照 'snap-report'")
    reclaimed = gc.sweep()
    print("\n== 5. 回收墓碑 ==")
    for tomb in reclaimed:
        print(f"  [已回收] {tomb.table}/{tomb.pk} 删除点={tomb.delete_seq} tx={tomb.tx_id}")
    print(f"  回收水位 reclaimed_horizon = {store.reclaimed_horizon}")

    print("\n== 6. 迟到旧插入不得复活已删除实体 ==")
    for event in load_fixture(LATE_FIXTURE):
        result = store.apply(event)
        print(f"  seq={event.commit_seq} insert {event.table}/{event.pk} -> {result.outcome} ({result.detail})")
    print(f"  users/u-2 当前值: {store.get('users', 'u-2')} (仍为已删除)")

    print("\n== 7. 新消费者必须从安全快照开始 ==")
    try:
        group.register("bi-new", start_seq=2)
    except MembershipError as exc:
        print(f"  [拒绝] {exc}")
    group.register("bi-new", start_seq=store.reclaimed_horizon)
    print(f"  [接受] 'bi-new' 从安全快照 seq={store.reclaimed_horizon} 开始")

    print("\n== 8. 最终状态 ==")
    print(f"  存活行: users/u-1={store.get('users', 'u-1')}, users/u-2={store.get('users', 'u-2')}")
    print(f"  待回收墓碑: {store.tombstones()}")


if __name__ == "__main__":
    main()
