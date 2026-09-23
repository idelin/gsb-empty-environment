"""运行样例：一致性快照 + 扫描期变更缓存 + 水位后回放 + 原子发布。

用法：python3 demo.py
"""

from __future__ import annotations

import tempfile
from pathlib import Path

from cdc_sync import SnapshotSynchronizer, SourceDatabase, TargetDatabase
from cdc_sync.fixture import group_transactions, load_fixture

FIXTURE = Path(__file__).parent / "fixtures" / "changes.json"


def show(title: str, rows: dict) -> None:
    print(f"  {title}")
    for pk in sorted(rows):
        print(f"    {pk}: {rows[pk]}")
    if not rows:
        print("    (空)")


def main() -> None:
    fixture = load_fixture(FIXTURE)
    tmp = tempfile.TemporaryDirectory()
    source = SourceDatabase(str(Path(tmp.name) / "source.db"))
    target = TargetDatabase(str(Path(tmp.name) / "target.db"))

    print("== 1. 快照前：应用夹具 pre_snapshot 事务 ==")
    for tx in group_transactions(fixture["pre_snapshot"]):
        assigned = source.apply_transaction(tx)
        print(f"  提交 {tx[0].tx_id} -> commit_seq={assigned[0].commit_seq}")
    print(f"  当前最大 commit_seq = {source.max_commit_seq()}")

    pending = group_transactions(fixture["during_scan"])
    injected: list[str] = []

    def during_scan(_rows_staged: int) -> None:
        # 模拟扫描快照期间源库仍在接受新写入（每次回调注入至多两个事务）。
        for _ in range(2):
            if pending:
                tx = pending.pop(0)
                source.apply_transaction(tx)
                injected.append(tx[0].tx_id)

    print("\n== 2. 执行同步（扫描期间并发注入 during_scan 事务） ==")
    sync = SnapshotSynchronizer(source, target)
    report = sync.run(during_scan=during_scan)
    print(f"  水位 watermark        = {report.watermark}")
    print(f"  扫描结束时高水位       = {report.high_water}")
    print(f"  快照行数              = {report.snapshot_rows}")
    print(f"  扫描期间注入的事务     = {', '.join(injected)}")
    print(f"  缓存的水位后变更       = {report.cached_events} 条")
    print(f"  回放：应用 {report.applied} 条，幂等跳过 {report.skipped} 条")
    print(f"  发布表                = {', '.join(report.tables)}")

    print("\n== 3. 发布后的目标库（live） ==")
    for tbl in target.live_tables():
        show(f"[{tbl}]", target.live_rows(tbl))
    tombs = target.live_tombstones()
    print(f"  墓碑（防复活）: {sorted(tombs) if tombs else '(无)'}")

    print("\n== 4. 与源库最新状态逐表比对 ==")
    ok = True
    for tbl in source.tables():
        same = target.live_rows(tbl) == source.table_rows(tbl)
        ok = ok and same
        print(f"  {tbl}: {'一致' if same else '不一致!'}")
    print(f"  结论: {'目标 == 源，无双计、无漏变更' if ok else '存在偏差!'}")

    print("\n== 5. 快照失败：不发布混合状态 ==")
    before = {t: target.live_rows(t) for t in target.live_tables()}

    def boom(_n: int) -> None:
        raise RuntimeError("模拟快照扫描中途失败")

    try:
        sync.run(during_scan=boom)
    except RuntimeError as exc:
        print(f"  同步按预期失败: {exc}")
    after = {t: target.live_rows(t) for t in target.live_tables()}
    print(f"  已发布状态保持不变: {before == after}")

    print("\n== 6. 重跑同步：结果可重复（幂等） ==")
    report2 = sync.run()
    same = all(target.live_rows(t) == source.table_rows(t) for t in source.tables())
    print(f"  第二次同步后目标仍与源一致: {same}")
    print(f"  第二次报告: watermark={report2.watermark}, "
          f"快照行={report2.snapshot_rows}, 应用={report2.applied}, 跳过={report2.skipped}")

    source.close()
    target.close()
    tmp.cleanup()


if __name__ == "__main__":
    main()
