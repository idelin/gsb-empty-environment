"""校验引擎：前值验证、冲突隔离、事务原子性、挂起与解决。

边界约定：
- 事务原子性：一个事务内任一 op 冲突 => 该事务整体失败，已推演的部分全部丢弃，
  绝不半提交，也绝不盲目后写覆盖（no last-write-wins）。
- 冲突隔离：失败事务触及的键被隔离（quarantine）；后续触碰这些键的事务挂起等待
  人工/外部解决；不触碰隔离键的独立事务照常校验提交，互不影响。
"""

from __future__ import annotations

import copy
from typing import Any

from .models import (
    MISSING,
    ChangeOp,
    ConflictKind,
    FieldConflict,
    Key,
    OpConflict,
    Transaction,
    TxResult,
    TxStatus,
)
from .store import VersionedStore


class Engine:
    def __init__(self) -> None:
        self.store = VersionedStore()
        self.quarantined: set[Key] = set()
        self.held: list[Transaction] = []
        self._seen: set[str] = set()
        self.results: list[TxResult] = []

    # ------------------------------------------------------------------ API

    def submit(self, tx: Transaction) -> TxResult:
        """按提交序号处理一个事务（调用方负责按 commit_seq 顺序投递）。"""
        if tx.tx_id in self._seen:
            result = TxResult(tx.tx_id, tx.commit_seq, TxStatus.DUPLICATE,
                              detail="tx_id 已处理，幂等跳过")
            self.results.append(result)
            return result
        self._seen.add(tx.tx_id)

        blocked_by = sorted(k for k in self._tx_keys(tx) if k in self.quarantined)
        if blocked_by:
            self.held.append(tx)
            result = TxResult(tx.tx_id, tx.commit_seq, TxStatus.HELD,
                              detail=f"等待隔离键解决: {blocked_by}")
            self.results.append(result)
            return result

        result = self._process(tx)
        self.results.append(result)
        return result

    def resolve(self, *keys: Key) -> list[TxResult]:
        """解除指定键（缺省为全部）的隔离，并按提交序号重试被挂起的事务。"""
        targets = set(keys) if keys else set(self.quarantined)
        self.quarantined -= targets

        retryable = [t for t in self.held if not self._tx_keys(t) & self.quarantined]
        self.held = [t for t in self.held if t not in retryable]
        out: list[TxResult] = []
        for tx in sorted(retryable, key=lambda t: t.commit_seq):
            result = self._process(tx)
            self.results.append(result)
            out.append(result)
        return out

    # ------------------------------------------------------------- internal

    @staticmethod
    def _tx_keys(tx: Transaction) -> set[Key]:
        return {op.key for op in tx.ops}

    def _process(self, tx: Transaction) -> TxResult:
        """先全量推演校验（按声明顺序），全部通过才原子落库。"""
        scratch = self.store.snapshot()
        conflicts: list[OpConflict] = []

        for index, op in enumerate(tx.ops):
            row = scratch.get(op.key)
            mismatched = self._validate_op(op, row)
            if mismatched:
                conflicts.append(OpConflict(
                    op_index=index,
                    kind=self._classify(op),
                    fields=tuple(mismatched),
                ))
            else:
                merged = dict(row) if row else {}
                merged.update(copy.deepcopy(dict(op.after)))
                scratch[op.key] = merged

        if conflicts:
            # 原子性：整体失败，scratch 丢弃；隔离本事务触及的全部键。
            self.quarantined |= self._tx_keys(tx)
            return TxResult(tx.tx_id, tx.commit_seq, TxStatus.FAILED_CONFLICT,
                            conflicts=tuple(conflicts),
                            detail="事务整体失败，键已隔离")

        for op in tx.ops:
            self.store.apply(op.key, op.after)
        return TxResult(tx.tx_id, tx.commit_seq, TxStatus.COMMITTED)

    @staticmethod
    def _validate_op(op: ChangeOp, row: dict[str, Any] | None) -> list[FieldConflict]:
        """字段级部分前值校验：只校验 before 提供的字段。"""
        out: list[FieldConflict] = []
        for name, expected in op.before.items():
            actual: Any = MISSING
            if row is not None and name in row:
                actual = row[name]
            if actual is MISSING or actual != expected:
                out.append(FieldConflict(op.table, op.pk, name, expected, actual))
        return out

    def _classify(self, op: ChangeOp) -> ConflictKind:
        matched = self.store.matches_history(op.key, op.before)
        if matched is not None and not self.store.matches(op.key, op.before):
            return ConflictKind.STALE_BEFORE
        if self.store.get(op.key) is None:
            return ConflictKind.ROW_MISSING
        return ConflictKind.MISSING_EVENT
