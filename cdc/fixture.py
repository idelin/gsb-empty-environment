"""本地夹具数据源：版本化行存储 + 仅追加变更日志，不接消息平台。

- 每次变更分配单调递增的 commit_seq（提交序号）并追加到日志。
- 每行保留 (commit_seq, 行值|None) 版本链，可重建任意水位处的一致性视图。
- snapshot() 在扫描期间通过 during_scan 回调注入并发变更，
  且可通过 fail_snapshot 注入扫描失败，用于验证不发布混合状态。
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Callable, Optional

from .events import OP_DELETE, ChangeEvent


class SnapshotError(Exception):
    """快照扫描失败。"""


@dataclass
class SnapshotResult:
    watermark: int  # 一致性水位：快照开始时已提交的最大 commit_seq
    rows: list  # [(table, pk, row_dict)]，水位处的一致性视图


def _row_at(history: list, seq: int) -> Optional[dict]:
    row = None
    for commit_seq, value in history:
        if commit_seq > seq:
            break
        row = value
    return None if row is None else dict(row)


class FixtureSource:
    def __init__(self) -> None:
        self._seq = 0
        self._txn = 0
        self._log: list[ChangeEvent] = []
        self._versions: dict[str, dict[Any, list]] = {}
        self.fail_snapshot = False

    @property
    def commit_seq(self) -> int:
        return self._seq

    def _next_txn_id(self) -> str:
        self._txn += 1
        return f"txn-{self._txn}"

    def apply_change(
        self,
        table: str,
        pk: Any,
        op: str,
        before: Optional[dict] = None,
        after: Optional[dict] = None,
        txn_id: Optional[str] = None,
    ) -> ChangeEvent:
        """在源库提交一笔变更，返回产生的 ChangeEvent。"""
        self._seq += 1
        event = ChangeEvent(
            txn_id=txn_id or self._next_txn_id(),
            table=table,
            pk=pk,
            op=op,
            before=before,
            after=after,
            commit_seq=self._seq,
        )
        self._log.append(event)
        history = self._versions.setdefault(table, {}).setdefault(pk, [])
        history.append((self._seq, None if op == OP_DELETE else dict(after)))
        return event

    def changes_since(self, watermark: int) -> list[ChangeEvent]:
        """拉取水位之后的变更（模拟订阅后续日志）。"""
        return [e for e in self._log if e.commit_seq > watermark]

    def snapshot(
        self, during_scan: Optional[Callable[[int], None]] = None
    ) -> SnapshotResult:
        """读取带一致性水位的全量快照。

        扫描键集合在开始时冻结，行值取水位处版本，因此扫描期间的
        并发变更不会混入快照。fail_snapshot 时扫描中途抛 SnapshotError。
        """
        watermark = self._seq
        rows = []
        frozen = {t: list(pks.items()) for t, pks in self._versions.items()}
        scanned = 0
        for table, pk_entries in frozen.items():
            for pk, history in pk_entries:
                row = _row_at(history, watermark)
                if row is not None:
                    rows.append((table, pk, row))
                scanned += 1
                if during_scan is not None:
                    during_scan(scanned)
                if self.fail_snapshot and scanned >= 2:
                    raise SnapshotError("snapshot scan failed (fixture)")
        return SnapshotResult(watermark=watermark, rows=rows)

    def live_state(self) -> dict:
        """源库当前最新状态，用于测试对账。"""
        state: dict[str, dict] = {}
        for table, pks in self._versions.items():
            for pk, history in pks.items():
                row = _row_at(history, self._seq)
                if row is not None:
                    state.setdefault(table, {})[pk] = row
        return state
