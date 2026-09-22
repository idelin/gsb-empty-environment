"""核心数据模型与胜出规则（不依赖索引，索引与参考实现共享）。

两条时间轴
----------
* 业务时间 (valid time)：事件实际发生/生效的时间。记录带左闭右开区间
  ``[vstart, vend)``，``vend = INF`` 表示开放终点。
* 录入时间 (record/system time)：证据进入账本的时刻 ``recorded_at``。
  查询携带观察时刻 ``as_of``，只能看到 ``recorded_at <= as_of`` 的证据。

更正/撤销/补录全部以追加记录实现，历史永不就地修改。
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from typing import Any, Generic, Iterable, Optional, TypeVar

__all__ = [
    "INF",
    "Op",
    "Status",
    "Record",
    "Segment",
    "choose_winner",
]

_T = TypeVar("_T")


class _Infinity:
    """表示开放区间右端点的 +∞，与任意可比较时间类型有序。"""

    _instance: "_Infinity | None" = None

    def __new__(cls) -> "_Infinity":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __lt__(self, other: object) -> bool:
        return False  # +∞ 不小于任何对象（包括自身）

    def __le__(self, other: object) -> bool:
        return isinstance(other, _Infinity)

    def __gt__(self, other: object) -> bool:
        return not isinstance(other, _Infinity)

    def __ge__(self, other: object) -> bool:
        return True

    def __repr__(self) -> str:
        return "INF"

    def __eq__(self, other: object) -> bool:
        return isinstance(other, _Infinity)

    def __hash__(self) -> int:
        return hash("+inf-sentinel")


INF = _Infinity()


class Op(str, Enum):
    """证据种类。"""

    ASSERT = "assert"        # 录入一个值（含更正、补录；是否覆盖由 replaces_id 决定）
    RETRACT = "retract"      # 撤销：区间在更新的观察时刻变为“无有效值”


class Status(str, Enum):
    """查询结果状态。"""

    PRESENT = "present"      # 有生效值
    RETRACTED = "retracted"  # 证据存在但已被撤销
    UNKNOWN = "unknown"      # 观察时刻之前没有任何已录入证据


@dataclass(frozen=True, slots=True)
class Record(Generic[_T]):
    """一条不可变证据。

    属性
    ----
    rid: 记录唯一标识，决定同一 ``recorded_at`` 并列时的确定次序。
    object_id: 对象标识。
    vstart, vend: 业务有效区间 ``[vstart, vend)``，左闭右开。
    recorded_at: 录入（生效）时间，查询可见性的唯一依据。
    op: ``ASSERT`` 带值；``RETRACT`` 的 ``value`` 必须为 ``None``。
    value: 业务值。
    replaces_id: 更正/撤销的直接前驱；同一对象、相同业务区间。
        非空表示“从 recorded_at 起，目标记录被本记录取代”。
    """

    rid: str
    object_id: Any
    vstart: Any
    vend: Any
    recorded_at: Any
    op: Op
    value: Optional[_T] = None
    replaces_id: Optional[str] = None

    def covers(self, t: Any) -> bool:
        """业务时间点 ``t`` 是否落在 ``[vstart, vend)`` 内。"""
        return self.vstart <= t < self.vend

    def visible(self, as_of: Any) -> bool:
        """观察时刻 ``as_of`` 之前（含）是否已录入。"""
        return self.recorded_at <= as_of


@dataclass(frozen=True, slots=True)
class Segment(Generic[_T]):
    """时间轴上某一段的生效结果。"""

    vstart: Any
    vend: Any
    status: Status
    value: Optional[_T] = None
    rid: Optional[str] = None  # 生效记录（RETRACT 时为撤销记录本身）


def choose_winner(candidates: Iterable[Record[_T]]) -> Optional[Record[_T]]:
    """从覆盖同一点、且彼此可见的候选中选出唯一胜出记录。

    规则（与记录到达/遍历顺序无关）：

    1. 链式失效：若候选中存在 ``replaces_id`` 指向另一条候选，则被指向者
       不是“链头”，先剔除；所有 ``replaces_id`` 目标必须同样可见且覆盖该点。
    2. 链头之间按 ``(recorded_at, rid)`` 字典序取最大：
       - 录入更晚的证据胜出（更正自然覆盖旧值）；
       - 同一时刻录入时，按 ``rid`` 确定地破并列。
    3. 胜出记录为 ``RETRACT`` 时结果是“已撤销”（无值），否则取其值。

    没有候选时返回 ``None``。
    """
    pool = list(candidates)
    superseded = {r.replaces_id for r in pool if r.replaces_id is not None}
    heads = [r for r in pool if r.rid not in superseded]
    if not heads:
        return None
    return max(heads, key=lambda r: (r.recorded_at, r.rid))
