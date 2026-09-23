"""事件模型与错误类型。

变更事件来自本地夹具(见 fixtures.py),不依赖任何消息平台。
每个 Change 携带: 事务ID、表、主键、前值、后值、事务内序号;
每个 Commit 携带全局提交序号 commit_seq。
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Optional, Tuple, Union

# 行的不可变表示: 按列名排序的 (列, 值) 二元组; None 表示行不存在(删除)。
Value = Optional[Tuple[Tuple[str, object], ...]]


def freeze_row(row: Optional[dict]) -> Value:
    if row is None:
        return None
    return tuple(sorted(row.items()))


def thaw_row(value: Value) -> Optional[dict]:
    if value is None:
        return None
    return dict(value)


class StreamValidationError(Exception):
    """事件流本身无效(结构/模式违规), 与处理器运行错误分开报告。"""


class ProcessorError(Exception):
    """处理器在应用一条结构有效的事件时发现语义错误。"""


@dataclass(frozen=True)
class Begin:
    txn_id: int


@dataclass(frozen=True)
class Change:
    txn_id: int
    seq: int          # 事务内变更序号, 从 0 连续编号
    table: str
    pk: str
    before: Value
    after: Value


@dataclass(frozen=True)
class Commit:
    txn_id: int
    commit_seq: int   # 全局提交序号, 从 0 连续编号


@dataclass(frozen=True)
class Abort:
    txn_id: int


@dataclass(frozen=True)
class Crash:
    """崩溃点: 处理器丢弃全部易失状态后重启(随后伴随至少一次语义的重放)。"""


Event = Union[Begin, Change, Commit, Abort, Crash]
