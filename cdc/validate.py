"""事件流校验: 只负责"流是否有效", 抛出 StreamValidationError。

校验通过不代表处理器不会报错; 语义冲突(如同序号不同内容)属于
ProcessorError, 由处理器在应用时发现, 二者在 harness 中分开归类。
"""
from __future__ import annotations

from typing import Dict, Iterable, List, Set

from .events import (
    Abort,
    Begin,
    Change,
    Commit,
    Crash,
    Event,
    StreamValidationError,
)

_SCALAR = (str, int, float, bool)


def _check_value(value, where: str) -> None:
    if value is None:
        return
    if not isinstance(value, tuple) or not all(
        isinstance(pair, tuple)
        and len(pair) == 2
        and isinstance(pair[0], str)
        and isinstance(pair[1], _SCALAR)
        for pair in value
    ):
        raise StreamValidationError(f"{where}: 非法行值 {value!r}")


def validate_stream(events: Iterable[Event], tables) -> List[Event]:
    """校验整条流, 返回 list 形式; 任何违规抛 StreamValidationError。

    允许: 完全相同的重复事件(至少一次投递)、事务内 Change 乱序、
    Crash 出现在任意位置、崩溃后事务整体重放(重复 Begin)。
    要求: 每个事务有 Begin 且恰好一种终结(Commit 或 Abort);
    每个事务的 Change seq 去重后为 0..k-1; 各事务的 commit_seq
    全局唯一且连续为 0..m-1(同一 Commit 的重复投递不计)。
    """
    events = list(events)
    table_names = set(tables)
    begins: Set[int] = set()
    terms: Dict[int, set] = {}
    seqs: Dict[int, Set[int]] = {}

    for index, ev in enumerate(events):
        where = f"事件[{index}]"
        if isinstance(ev, Crash):
            continue
        if isinstance(ev, Begin):
            if not isinstance(ev.txn_id, int) or ev.txn_id < 0:
                raise StreamValidationError(f"{where}: 非法 txn_id {ev.txn_id!r}")
            begins.add(ev.txn_id)
            continue
        if isinstance(ev, Change):
            if not isinstance(ev.txn_id, int) or ev.txn_id < 0:
                raise StreamValidationError(f"{where}: 非法 txn_id {ev.txn_id!r}")
            if not isinstance(ev.seq, int) or ev.seq < 0:
                raise StreamValidationError(f"{where}: 非法 seq {ev.seq!r}")
            if ev.table not in table_names:
                raise StreamValidationError(f"{where}: 未知表 {ev.table!r}")
            if not isinstance(ev.pk, str) or not ev.pk:
                raise StreamValidationError(f"{where}: 非法主键 {ev.pk!r}")
            _check_value(ev.before, where)
            _check_value(ev.after, where)
            seqs.setdefault(ev.txn_id, set()).add(ev.seq)
            continue
        if isinstance(ev, Commit):
            if not isinstance(ev.commit_seq, int) or ev.commit_seq < 0:
                raise StreamValidationError(f"{where}: 非法 commit_seq {ev.commit_seq!r}")
            terms.setdefault(ev.txn_id, set()).add(("commit", ev.commit_seq))
            continue
        if isinstance(ev, Abort):
            terms.setdefault(ev.txn_id, set()).add(("abort",))
            continue
        raise StreamValidationError(f"{where}: 未知事件 {ev!r}")

    for txn_id in sorted(begins | set(terms) | set(seqs)):
        if txn_id not in begins:
            raise StreamValidationError(f"事务 {txn_id}: 缺少 Begin")
        txn_terms = terms.get(txn_id, set())
        if not txn_terms:
            raise StreamValidationError(f"事务 {txn_id}: 缺少 Commit/Abort")
        if len(txn_terms) > 1:
            raise StreamValidationError(f"事务 {txn_id}: 冲突的终结事件 {txn_terms!r}")
        got = seqs.get(txn_id, set())
        if got != set(range(len(got))):
            raise StreamValidationError(
                f"事务 {txn_id}: Change seq 不连续: {sorted(got)!r}"
            )

    commit_seqs = sorted(
        term[1] for txn_terms in terms.values() for term in txn_terms
        if term[0] == "commit"
    )
    if commit_seqs != list(range(len(commit_seqs))):
        raise StreamValidationError(
            f"commit_seq 不唯一或不连续: {sorted(commit_seqs)!r}"
        )
    return events
