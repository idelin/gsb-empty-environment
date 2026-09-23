"""运行样例：本地夹具事件流驱动事务缓冲应用器。

用法：
    python3.11 demo.py            # 临时目录存放持久化状态，结束后保留路径
    python3.11 demo.py <目录>     # 指定状态目录，便于手动检查 state.json
"""

from __future__ import annotations

import os
import sys
import tempfile

from fixtures import (
    FIRST_BATCH,
    SECOND_BATCH,
    T1_COMMIT,
    replay,
)
from txbuffer import Applier, Commit

LABELS = {
    "buffered": "已缓冲",
    "waiting": "等待清单",
    "paused": "就绪但遇序号缺口，暂停",
    "committed": "已原子提交",
    "duplicate": "重复，忽略",
    "aborted": "回滚丢弃",
    "unknown": "未知事务，忽略",
}


def show(applier: Applier, title: str) -> None:
    print(f"\n--- {title} ---")
    print(f"last_commit_seq = {applier.last_commit_seq}")
    for table in ("accounts", "ledger"):
        rows = applier.table_rows(table)
        print(f"表 {table}: {rows if rows else '（空或不存在）'}")


def describe(event) -> str:
    from txbuffer import Abort, Change
    if isinstance(event, Change):
        verb = "删除" if event.after is None else ("插入" if event.before is None else "更新")
        return f"Change tx={event.txid} seq={event.seqno} {event.table}[{event.key}] {verb}"
    if isinstance(event, Commit):
        return f"Commit tx={event.txid} commit_seq={event.commit_seq} 变更数={event.change_count}"
    if isinstance(event, Abort):
        return f"Abort  tx={event.txid}"
    return repr(event)


def main() -> None:
    state_dir = sys.argv[1] if len(sys.argv) > 1 else tempfile.mkdtemp(prefix="txdemo-")
    state_path = os.path.join(state_dir, "state.json")
    os.makedirs(state_dir, exist_ok=True)
    print(f"状态文件：{state_path}")

    print("\n========== 进程实例 1（崩溃前的乱序事件流） ==========")
    applier = Applier(state_path)
    for event, result in zip(FIRST_BATCH, replay(applier, FIRST_BATCH)):
        print(f"  {describe(event):62s} -> {LABELS[result]}")
    show(applier, "实例 1 状态（T4 半事务、T5 回滚、T6 因缺口暂停，均不可见）")
    print(f"paused = {applier.paused}")

    print("\n========== 模拟崩溃后重启：新实例只从 state.json 恢复 ==========")
    recovered = Applier(state_path)
    show(recovered, "恢复后状态（半事务 T4 的缓冲已消失，只剩 T1/T2/T3）")

    print("\n========== 进程实例 2（补齐 T4 清单与缺失的提交序号 5） ==========")
    for event, result in zip(SECOND_BATCH, replay(recovered, SECOND_BATCH)):
        print(f"  {describe(event):62s} -> {LABELS[result]}")
    show(recovered, "实例 2 最终状态（T4/T7/T6 按 4/5/6 连续原子应用）")
    print(f"paused = {recovered.paused}")

    print("\n重复事务不二次生效：")
    print(f"  重放 T1 提交标记 -> {LABELS[recovered.handle_commit(T1_COMMIT)]}")
    print(f"  last_commit_seq 仍为 {recovered.last_commit_seq}")


if __name__ == "__main__":
    main()
