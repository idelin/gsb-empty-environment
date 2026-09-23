"""数据模型：事务、变更操作、冲突证据。"""

from __future__ import annotations

import enum
from dataclasses import dataclass, field
from typing import Any, Mapping


class _Missing:
    """哨兵：字段在行中不存在。缺失字段不等于 null。"""

    _instance = None

    def __new__(cls) -> "_Missing":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:
        return "MISSING"

    def __eq__(self, other: object) -> bool:
        return other is MISSING

    def __hash__(self) -> int:
        return hash("txguard.MISSING")


MISSING = _Missing()

Key = tuple[str, str]  # (table, pk)


class TxStatus(enum.Enum):
    COMMITTED = "committed"            # 校验通过，原子提交
    FAILED_CONFLICT = "failed"         # 前值冲突，事务整体失败（原子性）
    DUPLICATE = "duplicate"            # 重复提交，幂等跳过
    HELD = "held"                      # 触碰隔离键，挂起等待冲突解决


class ConflictKind(enum.Enum):
    STALE_BEFORE = "stale_before"      # 迟到旧版本：before 匹配历史快照但不匹配当前
    MISSING_EVENT = "missing_event"    # 疑似缺事件：before 与任何已知版本都对不上
    ROW_MISSING = "row_missing"        # before 期望的行不存在


@dataclass(frozen=True)
class ChangeOp:
    """单键变更：before/after 均为字段级部分值。

    before 只校验提供的字段；after 只覆盖提供的字段，未提供字段保留当前值。
    """

    table: str
    pk: str
    before: Mapping[str, Any] = field(default_factory=dict)
    after: Mapping[str, Any] = field(default_factory=dict)

    @property
    def key(self) -> Key:
        return (self.table, self.pk)


@dataclass(frozen=True)
class Transaction:
    """一个事务：同事务内多个 op 按声明顺序校验与生效。"""

    tx_id: str
    commit_seq: int
    ops: tuple[ChangeOp, ...]


@dataclass(frozen=True)
class FieldConflict:
    """最小冲突证据：仅包含不匹配的字段。"""

    table: str
    pk: str
    field: str
    expected: Any  # before 声明的前值
    actual: Any    # 当前实际值（可能为 MISSING）


@dataclass(frozen=True)
class OpConflict:
    """单个 op 的冲突：分类 + 最小字段证据。"""

    op_index: int
    kind: ConflictKind
    fields: tuple[FieldConflict, ...]


@dataclass(frozen=True)
class TxResult:
    tx_id: str
    commit_seq: int
    status: TxStatus
    conflicts: tuple[OpConflict, ...] = ()
    detail: str = ""

    def summary(self) -> str:
        line = f"{self.tx_id} seq={self.commit_seq} -> {self.status.value}"
        if self.detail:
            line += f" ({self.detail})"
        return line
