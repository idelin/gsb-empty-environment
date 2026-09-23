"""事件时间区间连接引擎（纯标准库）。

左/右两张变更表按 key 连接，事件时间之差绝对值不超过容差 ``tolerance``
即视为匹配，容差边界为闭区间（恰等于容差也连接）。

核心特性：
  * 一条左记录可匹配多条右记录（一左多右）。
  * 支持 INSERT / UPDATE / DELETE 变更（带前值、后值），DELETE 或 UPDATE
    的前值会撤回尚处于活跃状态的配对；已最终化配对的迟到撤回不会静默
    改写结果，而是进入迟到/越界报告。
  * 水位（watermark）推进时清理状态：当 ``ts + tolerance <= watermark``
    时该行不再可能产生新配对，对应配对最终化并释放状态。
  * 输出配对 ID 去重：同一会话中同一配对的 + / - 跳变只在状态真正翻转
    时下发；``live_pairs()`` 返回去重后的当前存活配对 ID。
  * 提交序号（commit seq）严格递增；重复序号、乱序到达（ts <= watermark
    的迟到数据）不改变状态，统一进入越界报告。

数据来源为本地夹具（``Change`` 对象列表），不连接任何消息平台。
"""

from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Optional

__all__ = [
    "Op",
    "Change",
    "change",
    "Watermark",
    "PairEvent",
    "LateReport",
    "ToleranceJoin",
    "LATE_INSERT",
    "LATE_DELETE",
    "STALE_DELETE",
    "UNKNOWN_TABLE",
]


class Op(str, Enum):
    """变更操作类型。"""

    INSERT = "INSERT"
    UPDATE = "UPDATE"
    DELETE = "DELETE"
    UPSERT = "UPSERT"


# 迟到 / 越界报告原因
LATE_INSERT = "LATE_INSERT"      # 插入或更新的后值越过当前水位
LATE_DELETE = "LATE_DELETE"      # 对一条已最终化（已清理）行的删除
STALE_DELETE = "STALE_DELETE"    # 对一条不存在 / 已删除行的删除
UNKNOWN_TABLE = "UNKNOWN_TABLE"  # 变更来自非左非右的表


@dataclass(frozen=True)
class Change:
    """一条本地夹具变更记录（CDC 风格）。

    属性：
        seq:      提交序号，严格递增；重复序号被视为重放并丢弃。
        table:    来源表名。
        pk:       主键值（表内唯一标识一行）。
        before:   前值镜像；INSERT 时为 None。
        after:    后值镜像；DELETE 时为 None。镜像中必须包含 key 列与 ts 列。
        op:       操作类型；为 None 时按 before/after 自动推断。
        event_ts: 该变更自身的事件时间；为 None 时取镜像中的 ts
                  （DELETE 取 before，其余取 after）。
    """

    seq: int
    table: str
    pk: Any
    before: Optional[dict[str, Any]]
    after: Optional[dict[str, Any]]
    op: Op = Op.UPSERT
    event_ts: Optional[int] = None

    def __post_init__(self) -> None:
        if not isinstance(self.seq, int) or self.seq < 0:
            raise ValueError("seq 必须是非负整数")
        if not self.table:
            raise ValueError("table 不能为空")
        inferred = self._infer_op()
        object.__setattr__(self, "op", inferred)
        if inferred is Op.UPDATE and self.event_ts is None:
            # 更新事件以前值时间作为其事件时间，保证撤回在水位边界处
            # 与旧行的最终化判定保持一致。
            object.__setattr__(self, "event_ts", self.before["ts"])

    def _infer_op(self) -> Op:
        if self.op not in (Op.INSERT, Op.UPDATE, Op.DELETE, Op.UPSERT):
            raise ValueError(f"未知操作类型: {self.op!r}")
        if self.before is None and self.after is None:
            raise ValueError("before 与 after 不能同时为 None")
        if self.op is not Op.UPSERT:
            if self.op is Op.INSERT and self.before is not None:
                raise ValueError("INSERT 不能携带 before")
            if self.op is Op.DELETE and self.after is not None:
                raise ValueError("DELETE 不能携带 after")
            if self.op is Op.UPDATE and (
                self.before is None or self.after is None
            ):
                raise ValueError("UPDATE 必须同时携带 before 与 after")
            return self.op
        # UPSERT：按镜像自动推断
        if self.before is None:
            return Op.INSERT
        if self.after is None:
            return Op.DELETE
        return Op.UPDATE

    @property
    def ts(self) -> int:
        """变更的事件时间（用于迟到判定）。"""
        if self.event_ts is not None:
            return self.event_ts
        image = self.after if self.after is not None else self.before
        return image["ts"]


def change(
    seq: int,
    table: str,
    pk: Any,
    before: Optional[dict[str, Any]],
    after: Optional[dict[str, Any]],
    op: Optional[Op] = None,
    event_ts: Optional[int] = None,
) -> Change:
    """便捷构造器；``op=None`` 时按 before/after 推断。"""

    return Change(seq, table, pk, before, after, op or Op.UPSERT, event_ts)


@dataclass(frozen=True, order=True)
class Watermark:
    """水位标记：与变更共用同一提交序号序列，便于在夹具中保序混合。"""

    seq: int
    ts: int


@dataclass(frozen=True)
class PairEvent:
    """配对结果变更（changelog 中的一条）。

    sign 为 ``+`` 表示配对建立，``-`` 表示配对撤回；
    同一 pair_id 的重复 + / - 不会重复下发（去重）。
    """

    pair_id: str
    sign: str
    left_pk: Any
    right_pk: Any
    key: Any
    left_ts: int
    right_ts: int
    seq: int


@dataclass(frozen=True)
class LateReport:
    """迟到 / 越界记录（旁路输出），绝不静默丢弃。"""

    seq: int
    table: str
    pk: Any
    key: Any
    event_ts: int
    watermark: Optional[int]
    op: Op
    reason: str
    detail: str = ""


@dataclass
class _Row:
    """内部行状态。"""

    side: str
    pk: Any
    key: Any
    ts: int
    seq: int
    frozen: bool = False


class ToleranceJoin:
    """按键、按事件时间容差连接左右变更流。

    参数：
        left_table / right_table: 左右来源表名。
        key_col: 两侧共用的连接键列名。
        ts_col:  事件时间列名（值为可比较的整数时间戳）。
        tolerance: 容差，``|left.ts - right.ts| <= tolerance`` 时连接，
                   必须是非负整数；恰等于容差（边界）包含。
    """

    def __init__(
        self,
        left_table: str,
        right_table: str,
        key_col: str = "key",
        ts_col: str = "ts",
        tolerance: int = 0,
    ) -> None:
        if tolerance < 0:
            raise ValueError("tolerance 不能为负")
        if not key_col or not ts_col:
            raise ValueError("key_col 与 ts_col 不能为空")
        self.left_table = left_table
        self.right_table = right_table
        self.key_col = key_col
        self.ts_col = ts_col
        self.tolerance = tolerance

        # side -> key -> {pk: _Row}，仅保留尚未清理的活跃行
        self._rows: dict[str, dict[Any, dict[Any, _Row]]] = {
            "L": {},
            "R": {},
        }
        # pair_id -> (left_pk, right_pk, key, left_ts, right_ts)
        self._active_pairs: dict[str, tuple] = {}
        # side -> pk -> set(pair_id)，撤回时按主键索引活跃配对
        self._index: dict[str, dict[Any, set[str]]] = {"L": {}, "R": {}}
        # 已最终化配对 ID（只增不减，用于最终结果去重）
        self._finalized: set[str] = set()
        # 已随水位清理的行主键（side -> set(pk)），用于区分迟到删除与无效删除
        self._cleaned_rows: dict[str, set[Any]] = {'L': set(), 'R': set()}
        # pair_id -> 最近下发的符号（+/-），保证 changelog 去重
        self._pair_state: dict[str, str] = {}
        self._watermark: Optional[int] = None
        self._seen_seq: set[int] = set()
        self.changelog: list[PairEvent] = []
        self.late: list[LateReport] = []

    # ------------------------------------------------------------------ #
    # 公共读接口
    # ------------------------------------------------------------------ #
    @property
    def watermark(self) -> Optional[int]:
        return self._watermark

    def output(self) -> list[PairEvent]:
        """去重后的配对 changelog（+ 建立 / - 撤回），按产生顺序排列。"""

        return list(self.changelog)

    def live_pairs(self) -> list[str]:
        """当前仍存活（已建立且未撤回）的配对 ID，按字典序去重返回。"""

        live = {
            event.pair_id
            for event in self.changelog
            if self._pair_state.get(event.pair_id) == "+"
        }
        return sorted(live)

    def finalized_pairs(self) -> list[str]:
        """已随水位最终化的配对 ID（最终结果视图），按字典序返回。"""

        return sorted(self._finalized)

    def pair_id(self, left_pk: Any, right_pk: Any) -> str:
        """由左右主键生成稳定的配对 ID。"""

        return f"{left_pk}|{right_pk}"

    # ------------------------------------------------------------------ #
    # 摄入
    # ------------------------------------------------------------------ #
    def ingest(self, item: Change | Watermark) -> None:
        """按提交序号顺序摄入一条变更或一个水位标记。"""

        if isinstance(item, Watermark):
            self.ingest_watermark(item.seq, item.ts)
            return
        if not isinstance(item, Change):
            raise TypeError("只接受 Change 或 Watermark")
        if item.seq in self._seen_seq:
            # 提交序号重放：幂等忽略，保证恰好一次的处理语义。
            return
        side = self._side_of(item.table)
        if side is None:
            image = item.after if item.after is not None else item.before
            key = image.get(self.key_col) if image is not None else None
            self._report(item, key, UNKNOWN_TABLE, "非左/右表，已忽略")
            self._seen_seq.add(item.seq)
            return
        self._seen_seq.add(item.seq)
        self._apply(side, item)

    def ingest_watermark(self, seq: int, ts: int) -> None:
        """推进水位；同一序号或回退的水位被忽略。"""

        if seq in self._seen_seq:
            return
        self._seen_seq.add(seq)
        if self._watermark is not None and ts <= self._watermark:
            return
        self._watermark = ts
        self._advance_watermark()

    def _side_of(self, table: str) -> Optional[str]:
        if table == self.left_table:
            return "L"
        if table == self.right_table:
            return "R"
        return None

    # ------------------------------------------------------------------ #
    # 变更处理
    # ------------------------------------------------------------------ #
    def _apply(self, side: str, item: Change) -> None:
        # 先处理前值撤回（DELETE / UPDATE），再处理后值建立（INSERT / UPDATE）
        if item.before is not None:
            before_key, before_ts = self._read_image(side, item.before)
            self._retract(side, item, before_key, before_ts)
        if item.after is not None:
            after_key, after_ts = self._read_image(side, item.after)
            self._put(side, item, after_key, after_ts)

    def _read_image(
        self, side: str, image: dict[str, Any]
    ) -> tuple[Any, int]:
        if self.key_col not in image:
            raise ValueError(f"镜像缺少连接键列 {self.key_col!r}: {image!r}")
        if self.ts_col not in image:
            raise ValueError(f"镜像缺少时间列 {self.ts_col!r}: {image!r}")
        return image[self.key_col], image[self.ts_col]

    def _report(
        self,
        item: Change,
        key: Any,
        reason: str,
        detail: str,
    ) -> None:
        self.late.append(
            LateReport(
                seq=item.seq,
                table=item.table,
                pk=item.pk,
                key=key,
                event_ts=item.ts,
                watermark=self._watermark,
                op=item.op,
                reason=reason,
                detail=detail,
            )
        )

    def _retract(
        self, side: str, item: Change, key: Any, ts: int
    ) -> None:
        rows_by_key = self._rows[side].get(key, {})
        row = rows_by_key.get(item.pk)
        if row is not None and not row.frozen:
            # 行仍在活跃状态（配对尚未最终化）：撤回有效
            self._erase(side, item.pk, key, item.seq)
            return
        # 行已随水位清理（结果已最终化）或从未存在 / 已被删除：
        # 绝不静默改写最终结果，统一进入迟到 / 越界报告。
        if item.pk in self._cleaned_rows[side]:
            reason = LATE_DELETE
            detail = "目标行已随水位清理并最终化，迟到删除不会静默撤回结果"
        else:
            reason = STALE_DELETE
            detail = "目标行不存在或已被删除"
        self._report(item, key, reason, detail)

    def _put(
        self,
        side: str,
        item: Change,
        key: Any,
        ts: int,
    ) -> None:
        if self._watermark is not None and ts <= self._watermark:
            self._report(
                item,
                key,
                LATE_INSERT,
                "事件时间不晚于当前水位，属于迟到越界数据，不入状态",
            )
            return

        rows_by_key = self._rows[side].setdefault(key, {})
        existing = rows_by_key.get(item.pk)
        if existing is not None:
            if existing.seq >= item.seq:
                # 旧版本保护：更高提交序号的行已在状态中
                return
            if existing.ts == ts and existing.key == key:
                # 重复投递的幂等写入：连接键与事件时间未变，
                # 配对关系不变，原地刷新行（不产生 -/+ 抖动，也不重复配对）
                existing.seq = item.seq
                return
            # 连接键或事件时间改变：撤回旧配对后按新镜像重建
            self._erase(side, item.pk, key, item.seq)

        row = _Row(side=side, pk=item.pk, key=key, ts=ts, seq=item.seq)
        self._rows[side][key][item.pk] = row

        other = "R" if side == "L" else "L"
        for candidate in self._rows[other].get(key, {}).values():
            if candidate.frozen:
                continue
            if abs(candidate.ts - ts) <= self.tolerance:
                self._link(row, candidate, item.seq)

    def _erase(self, side: str, pk: Any, key: Any, seq: int) -> None:
        rows_by_key = self._rows[side].get(key)
        if rows_by_key is not None:
            rows_by_key.pop(pk, None)
            if not rows_by_key:
                self._rows[side].pop(key, None)
        for pair_id in list(self._index[side].get(pk, ())):
            info = self._active_pairs.pop(pair_id, None)
            if info is not None:
                left_pk, right_pk, pair_key, left_ts, right_ts = info
                self._emit(
                    pair_id,
                    "-",
                    left_pk,
                    right_pk,
                    pair_key,
                    left_ts,
                    right_ts,
                    seq,
                )
        self._index[side].pop(pk, None)

    def _link(
        self, row_a: _Row, row_b: _Row, seq: int
    ) -> None:
        left = row_a if row_a.side == "L" else row_b
        right = row_b if row_a.side == "L" else row_a
        pair_id = self.pair_id(left.pk, right.pk)
        if pair_id in self._active_pairs or pair_id in self._finalized:
            # 配对去重：活跃中或已最终化的同一对不重复建立
            return
        self._active_pairs[pair_id] = (
            left.pk,
            right.pk,
            left.key,
            left.ts,
            right.ts,
        )
        self._index["L"].setdefault(left.pk, set()).add(pair_id)
        self._index["R"].setdefault(right.pk, set()).add(pair_id)
        self._emit(
            pair_id,
            "+",
            left.pk,
            right.pk,
            left.key,
            left.ts,
            right.ts,
            seq,
        )

    def _emit(
        self,
        pair_id: str,
        sign: str,
        left_pk: Any,
        right_pk: Any,
        key: Any,
        left_ts: int,
        right_ts: int,
        seq: int,
    ) -> None:
        if self._pair_state.get(pair_id) == sign:
            return  # changelog 去重：符号未翻转不下发
        self._pair_state[pair_id] = sign
        self.changelog.append(
            PairEvent(
                pair_id=pair_id,
                sign=sign,
                left_pk=left_pk,
                right_pk=right_pk,
                key=key,
                left_ts=left_ts,
                right_ts=right_ts,
                seq=seq,
            )
        )

    # ------------------------------------------------------------------ #
    # 水位清理与最终化
    # ------------------------------------------------------------------ #
    def _advance_watermark(self) -> None:
        assert self._watermark is not None
        wm = self._watermark
        # 1) 冻结并清理到期行（ts + tolerance <= wm）
        for side in ("L", "R"):
            for key in list(self._rows[side]):
                for pk in list(self._rows[side][key]):
                    row = self._rows[side][key][pk]
                    # 闭区间边界：ts + tolerance == wm 时也冻结
                    if not row.frozen and row.ts + self.tolerance <= wm:
                        self._freeze_row(row)
        # 2) 兜底最终化：两侧事件时间都已封闭的活跃配对。
        #    配对两侧时间差可能使其中一侧先被冻结移除，此时不再会有
        #    任何行触发 _freeze_row，因此这里按 max(ts)+tol 统一裁决。
        self._finalize_ready_pairs()

    def _finalize_ready_pairs(self) -> None:
        wm = self._watermark
        for pair_id in list(self._active_pairs):
            info = self._active_pairs.get(pair_id)
            if info is None:
                continue
            left_pk, right_pk, pair_key, left_ts, right_ts = info
            # 最终化条件：较晚一侧也已封闭，即
            # max(左ts, 右ts) + tolerance <= watermark（闭区间）。
            # 在此之前，迟到对侧仍可能建立/撤回该配对。
            if max(left_ts, right_ts) + self.tolerance <= wm:
                self._active_pairs.pop(pair_id, None)
                self._finalized.add(pair_id)
                self._index["L"].get(left_pk, set()).discard(pair_id)
                self._index["R"].get(right_pk, set()).discard(pair_id)

    def _freeze_row(self, row: _Row) -> None:
        row.frozen = True
        self._cleaned_rows[row.side].add(row.pk)
        self._rows[row.side][row.key].pop(row.pk, None)
        if not self._rows[row.side].get(row.key):
            self._rows[row.side].pop(row.key, None)
        self._index[row.side].pop(row.pk, None)
