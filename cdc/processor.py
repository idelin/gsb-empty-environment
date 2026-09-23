"""被测处理器: 增量物化器。

语义:
- 每个事务的 Change 按 seq 连续前缀应用到事务私有 overlay;
  乱序到达的先缓冲, 完全相同的重复事件去重。
- Commit 把 overlay 合并进已提交的持久状态; Abort 丢弃。
- Crash 清空全部易失状态(打开的事务、暂存事件),
  已提交状态与去重日志是持久的, 重启后重放可继续。
- 语义违规(未知事务的 Commit、同事务同 seq 不同内容、
  commit_seq 冲突、已终结事务收到新 Change 等)抛 ProcessorError。

另附 ArrivalOrderProcessor: 一个故意简化(错误)的实现,
忽略 seq 直接按到达顺序应用, 用于演示差分失败与自动缩减。
"""
from __future__ import annotations

from typing import Dict, List, Tuple

from .events import Abort, Begin, Change, Commit, Crash, Event, ProcessorError


class _TxnBuffer:
    __slots__ = ("buffered", "applied", "next_seq", "overlay")

    def __init__(self) -> None:
        self.buffered: Dict[int, Change] = {}   # 已到但未连续的 Change
        self.applied: Dict[int, Change] = {}    # 已按序并入 overlay 的 Change
        self.next_seq = 0
        self.overlay: Dict[Tuple[str, str], object] = {}


class IncrementalProcessor:
    def __init__(self, tables) -> None:
        self._committed: Dict[str, Dict[str, object]] = {t: {} for t in tables}
        # 持久去重日志: 已终结事务及其 Change seq 集合 / commit_seq。
        self._finished: Dict[int, str] = {}
        self._finished_seqs: Dict[int, set] = {}
        self._commits: Dict[int, int] = {}
        self._used_commit_seqs: set = set()
        self._reset_volatile()

    def _reset_volatile(self) -> None:
        self._open: Dict[int, _TxnBuffer] = {}
        self._pending: Dict[int, List[Change]] = {}  # Begin 之前到达的 Change

    # -- 对外接口 ------------------------------------------------------
    def apply(self, event: Event) -> None:
        if isinstance(event, Crash):
            self._reset_volatile()
        elif isinstance(event, Begin):
            self._begin(event.txn_id)
        elif isinstance(event, Change):
            self._change(event)
        elif isinstance(event, Commit):
            self._commit(event.txn_id, event.commit_seq)
        elif isinstance(event, Abort):
            self._abort(event.txn_id)
        else:
            raise ProcessorError(f"未知事件: {event!r}")

    def run(self, events) -> None:
        for event in events:
            self.apply(event)

    def state(self) -> Dict[str, Dict[str, object]]:
        return {table: dict(rows) for table, rows in self._committed.items()}

    # -- 内部分派 ------------------------------------------------------
    def _begin(self, txn_id: int) -> None:
        if txn_id in self._finished:
            raise ProcessorError(f"事务 {txn_id} 已终结, 收到重复 Begin")
        if txn_id in self._open:
            return  # 重复 Begin, 忽略
        self._open[txn_id] = _TxnBuffer()
        for change in self._pending.pop(txn_id, []):
            self._buffer_change(self._open[txn_id], change)

    def _change(self, change: Change) -> None:
        if change.table not in self._committed:
            raise ProcessorError(f"未知表: {change.table!r}")
        if change.txn_id in self._finished:
            if change.seq in self._finished_seqs.get(change.txn_id, ()):
                return  # 已终结事务的重复重放, 忽略
            raise ProcessorError(
                f"事务 {change.txn_id} 已终结, 收到新 Change seq={change.seq}"
            )
        buffer = self._open.get(change.txn_id)
        if buffer is None:
            self._pending.setdefault(change.txn_id, []).append(change)
            return
        self._buffer_change(buffer, change)

    def _buffer_change(self, buffer: _TxnBuffer, change: Change) -> None:
        existing = buffer.buffered.get(change.seq) or buffer.applied.get(change.seq)
        if existing is not None:
            if existing != change:
                raise ProcessorError(
                    f"事务 {change.txn_id} seq={change.seq} 内容冲突: "
                    f"{existing!r} vs {change!r}"
                )
            return
        buffer.buffered[change.seq] = change
        while buffer.next_seq in buffer.buffered:
            contiguous = buffer.buffered.pop(buffer.next_seq)
            buffer.applied[contiguous.seq] = contiguous
            buffer.overlay[(contiguous.table, contiguous.pk)] = contiguous.after
            buffer.next_seq += 1

    def _commit(self, txn_id: int, commit_seq: int) -> None:
        previous = self._commits.get(txn_id)
        if previous is not None:
            if previous == commit_seq:
                return  # 重复 Commit, 忽略
            raise ProcessorError(
                f"事务 {txn_id} 冲突的 Commit: {previous} vs {commit_seq}"
            )
        if commit_seq in self._used_commit_seqs:
            raise ProcessorError(f"commit_seq {commit_seq} 被重复使用")
        buffer = self._open.pop(txn_id, None)
        if buffer is None:
            raise ProcessorError(f"未知事务 {txn_id} 的 Commit")
        for (table, pk), value in buffer.overlay.items():
            if value is None:
                self._committed[table].pop(pk, None)
            else:
                self._committed[table][pk] = value
        self._commits[txn_id] = commit_seq
        self._used_commit_seqs.add(commit_seq)
        self._finished[txn_id] = "commit"
        self._finished_seqs[txn_id] = set(buffer.applied) | set(buffer.buffered)

    def _abort(self, txn_id: int) -> None:
        outcome = self._finished.get(txn_id)
        if outcome is not None:
            if outcome == "abort":
                return  # 重复 Abort, 忽略
            raise ProcessorError(f"事务 {txn_id} 已提交, 收到 Abort")
        buffer = self._open.pop(txn_id, None)
        if buffer is None:
            raise ProcessorError(f"未知事务 {txn_id} 的 Abort")
        self._finished[txn_id] = "abort"
        self._finished_seqs[txn_id] = set(buffer.applied) | set(buffer.buffered)


class ArrivalOrderProcessor(IncrementalProcessor):
    """故意简化的错误实现: 忽略 seq, 按到达顺序直接覆盖 overlay。

    同事务同一主键的多个 Change 乱序到达时会产生错误的最终状态,
    仅用于演示差分失败与自动缩减。
    """

    def _buffer_change(self, buffer: _TxnBuffer, change: Change) -> None:
        existing = buffer.applied.get(change.seq)
        if existing is not None:
            if existing != change:
                raise ProcessorError(
                    f"事务 {change.txn_id} seq={change.seq} 内容冲突"
                )
            return
        buffer.applied[change.seq] = change
        buffer.overlay[(change.table, change.pk)] = change.after
