"""运行样例：后台重建 -> 增量缓存 -> 追平 -> 原子切换 -> 失败兜底。

用法：python3.11 main.py
"""

from __future__ import annotations

import time
from pathlib import Path

from cdc_view import BaseStore, ViewManager, load_events, load_snapshot

FIXTURES = Path(__file__).parent / "fixtures"


def show(tag: str, manager: ViewManager) -> None:
    view = manager.acquire()
    print(f"  [{tag}] version=v{view.version} watermark={view.watermark} "
          f"stats={view.stats()}")


def main() -> None:
    tables, watermark = load_snapshot(FIXTURES / "snapshot.json")
    events = load_events(FIXTURES / "changes.json")
    store = BaseStore(tables, watermark)
    manager = ViewManager()

    print("== 1. 引导 v1（初始快照 @%d）==" % watermark)
    manager.bootstrap(tables, watermark)
    show("v1 发布", manager)

    print("== 2. 重建前先到达 2 条变更（T1/T2），旧视图照常增量 ==")
    for event in events[:2]:
        store.apply(event)
        manager.apply_change(event)
    show("旧视图 v1", manager)

    print("== 3. 打固定快照，后台重建新视图（构建耗时 0.3s 模拟） ==")
    snap_tables, snap_wm = store.snapshot()
    print(f"  固定快照水位={snap_wm}")
    old_reader = manager.acquire()  # 老读者持有 v1
    thread = manager.start_rebuild(snap_tables, snap_wm, build_delay=0.3)

    print("== 4. 构建期间继续到达变更（含重复投递 T3/T6），进入缓存 ==")
    for event in events[2:]:
        store.apply(event)
        manager.apply_change(event)
        print(f"  缓存并应用 {event.txn_id} {event.table}#{event.pk} "
              f"seq={event.commit_seq}")
        time.sleep(0.03)
    print(f"  构建期间查询仍走旧视图: version=v{manager.current_version()} "
          f"(重建未追平，未发布)")

    thread.join()
    print("== 5. 追平后原子切换 ==")
    print(f"  重建状态={manager.last_rebuild_status}")
    show("新视图", manager)

    print("== 6. 旧读者按旧版本继续可读 ==")
    print(f"  老读者句柄: version=v{old_reader.version} "
          f"watermark={old_reader.watermark} stats={old_reader.stats()}")

    print("== 7. 校验：切换后结果 == 从头单遍应用（无双计） ==")
    expected = {
        "orders": {"count": 4, "amount": 900},
        "customers": {"count": 3, "amount": 180},
    }
    actual = manager.acquire().stats()
    assert actual == expected, f"{actual} != {expected}"
    assert manager.acquire().watermark == events[-1].commit_seq
    print(f"  校验通过: {actual}")

    print("== 8. 重建失败兜底：旧视图继续可用 ==")
    before = manager.current_version()
    manager.start_rebuild(*store.snapshot(), fail=True).join()
    print(f"  重建状态={manager.last_rebuild_status} "
          f"原因={manager.last_rebuild_error}")
    assert manager.current_version() == before
    show("旧视图仍服务", manager)

    print("\n全部演示通过。")


if __name__ == "__main__":
    main()
