"""End-to-end walkthrough on synthetic data.

Run:  python3 -m alerting.demo

Scenarios:
 1. enter=10 / duration=5; samples 0:11, 6:12 fire; backfilled 3:8 retracts.
 2. fault recovers during suppression: no stale OPEN after the window lifts.
 3. three overlapping suppression rules; threshold rollback; same-time
    sample correction; real outbox restart after "recorded, unconfirmed".
 4. incremental state vs full-history replay are identical.
"""
from __future__ import annotations

import os
import tempfile

from alerting.engine import Engine
from alerting.outbox import CrashAt, Pump, Transport


def banner(title):
    print("\n" + "=" * 70)
    print(title)
    print("=" * 70)


def show(e, label):
    print(f"\n-- {label}")
    for idx, a in enumerate(e.alert_snapshot(), start=1):
        (_, _, ver, start, end, mature, first_open,
         state, retracted, opened, revno) = a
        print(f"   告警#{idx} v{ver} [{start}..{end}] mature={mature} "
              f"first_open={first_open} state={state} rev={revno} "
              f"opened={opened} retracted={retracted}")
    kinds = e.notification_kinds()
    print("   通知事件:", kinds if kinds else "(无)")
    for r in e.revision_snapshot():
        aid, revno, change, reason, state, supersedes = r
        print(f"   审计 告警{aid} r{revno}: {change:<9} "
              f"({reason}) state={state} supersedes=r{supersedes}")


def main():
    workdir = tempfile.mkdtemp(prefix="alerting-demo-")
    db = os.path.join(workdir, "demo.db")

    banner("场景1: 进入阈值10 持续5; t=0:11, t=6:12 触发; 补 t=3:8 后撤销")
    e = Engine(db)
    e.add_threshold("cpu", valid_from=0, metric="cpu",
                    enter_at=10, exit_at=10, duration=5)
    e.ingest_sample(0, "cpu", 11)
    show(e, "t=0 值=11: 超阈值但持续不足 -> pending, 不通知")
    e.ingest_sample(6, "cpu", 12)
    show(e, "t=6 值=12: 0..5 持续已满 -> firing, OPEN 入 outbox")
    Pump(e).run()
    e.ingest_sample(3, "cpu", 8)
    show(e, "补数据 t=3 值=8: 真实故障在成熟前结束 -> RETRACT 纠正, "
            "原 OPEN 审计保留")

    banner("场景2: 抑制期间故障恢复, 解除抑制后不补发过时故障")
    db2 = os.path.join(workdir, "sup.db")
    e = Engine(db2)
    e.add_threshold("cpu", 0, "cpu", 10, 10, 5)
    e.ingest_sample(0, "cpu", 11)
    e.ingest_sample(6, "cpu", 12)
    Pump(e).run()
    e.add_suppression("maint", window_start=4, window_end=10,
                      metric="cpu", valid_from=4)
    show(e, "抑制窗口 [4,10): firing -> suppressed, 发 REPLACE")
    e.ingest_sample(8, "cpu", 0)
    show(e, "t=8 在抑制中恢复: suppressed -> resolved, 发 RESOLVE")
    e.ingest_sample(12, "cpu", 0)
    show(e, "t=12 抑制解除: 故障已死, 没有任何补发 OPEN")

    banner("场景3: 三条交叠抑制 + 阈值回滚 + 同时间更正 + outbox 重启")
    db3 = os.path.join(workdir, "all.db")
    e = Engine(db3)
    e.add_threshold("mem", 0, "mem", 10, 10, 3)
    e.add_suppression("a", 2, 8, "mem", valid_from=2)
    e.add_suppression("b", 5, 12, "mem", valid_from=5)
    e.add_suppression("c", 10, 20, "mem", valid_from=10)
    for t in range(0, 19):
        e.ingest_sample(t, "mem", 12)
    show(e, "三窗口并集 [2,20): 故障在抑制中成熟 -> suppressed, 零通知")
    e.ingest_sample(18, "mem", 0)
    e.ingest_sample(22, "mem", 5)
    show(e, "t=18 抑制中恢复; t=22 窗口早已结束: 仍零通知, 无过时补发")

    # threshold rollback on a second metric
    e.add_threshold("net", 0, "net", 10, 10, 5)
    for t in range(0, 16, 2):
        e.ingest_sample(t, "net", 12)
    Pump(e).run()
    e.add_threshold("net", 10, "net", 100, 100, 5)
    e.rollback_threshold("net", to_version=1, valid_from=14)
    show(e, "阈值 v2 收紧后 v3 回滚为 v1 参数: 只追加版本, 历史不被覆盖")

    # same-time correction + crash boundary
    e.ingest_sample(20, "net", 12)
    show(e, "t=20: 回滚后的新故障成熟 -> 新 OPEN")
    pump = Pump(e)
    pump.crash = CrashAt.AFTER_SEND
    try:
        pump.run()
    except RuntimeError as exc:
        print(f"\n-- 注入崩溃: {exc}")
    print("   崩溃后未确认条数:", e.pending_count())
    e2 = Engine(db3)  # 新进程, 同一个文件
    fresh_transport = Transport()
    Pump(e2, fresh_transport).run()
    print("-- 重启后投递内容(去重键):")
    for k, v in e2.notification_contents():
        print(f"   {k:<14} {v[2]} ({v[3]})")
    print("   待确认:", e2.pending_count(),
          " 传输层实际发送次数:", len(fresh_transport.sent),
          "(崩溃前已发送1次, 重启幂等重试)")

    banner("场景4: 增量状态 == 从不可变日志的完整重算")
    replay_db = os.path.join(workdir, "replay.db")
    import shutil
    shutil.copyfile(db3, replay_db)
    r = Engine.replay(replay_db)
    same_alerts = e2.alert_snapshot() == r.alert_snapshot()
    same_notes = e2.notification_contents() == r.notification_contents()
    same_revs = e2.revision_snapshot() == r.revision_snapshot()
    print(f"   告警状态一致: {same_alerts}")
    print(f"   通知事件一致: {same_notes}")
    print(f"   修订审计一致: {same_revs}")
    assert same_alerts and same_notes and same_revs

    print("\n演示数据库目录:", workdir)


if __name__ == "__main__":
    main()
