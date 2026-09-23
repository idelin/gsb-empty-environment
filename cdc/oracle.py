"""独立 oracle: 由整条事件流一次性重算出最终全量状态。

与被测的增量算法完全无关(不 import、不调用被测实现):
先按事务分组并去重, 再按 commit_seq 排序已提交事务,
最后按 seq 顺序把每个 Change 的 after 值落到一张全新的表上。
"""
from __future__ import annotations

from typing import Dict, Iterable

from .events import Abort, Change, Commit, Event
from .validate import validate_stream


def compute_final_state(events: Iterable[Event], tables) -> Dict[str, Dict[str, object]]:
    """返回 {表: {主键: 行值}}。流无效时抛 StreamValidationError。"""
    events = validate_stream(events, tables)

    commits: Dict[int, int] = {}
    aborted = set()
    changes: Dict[int, Dict[int, Change]] = {}
    for ev in events:
        if isinstance(ev, Commit):
            commits.setdefault(ev.txn_id, ev.commit_seq)
        elif isinstance(ev, Abort):
            aborted.add(ev.txn_id)
        elif isinstance(ev, Change):
            # 完全相同的重复投递去重; 同 seq 不同内容时取先到者,
            # 该冲突由处理器侧以 ProcessorError 报告, 与 oracle 无关。
            changes.setdefault(ev.txn_id, {}).setdefault(ev.seq, ev)

    state: Dict[str, Dict[str, object]] = {t: {} for t in tables}
    for txn_id in sorted(commits, key=lambda t: commits[t]):
        if txn_id in aborted:
            continue
        for seq in sorted(changes.get(txn_id, {})):
            change = changes[txn_id][seq]
            if change.after is None:
                state[change.table].pop(change.pk, None)
            else:
                state[change.table][change.pk] = change.after
    return state
