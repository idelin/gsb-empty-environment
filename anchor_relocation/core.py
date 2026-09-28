"""段落锚点重定位核心算法。

文本被视为 Unicode 码点序列；所有位置与长度均以码点为单位，
不使用 UTF-8 字节长度。位置语义为“码点之间的间隙”，
合法位置范围是 0..len(text)（含两端）。

批量编辑均以同一个基准版本的偏移量定义，由 TextHistory.apply
一次性原子提交：编辑之间存在严格区间重叠时整体拒绝、不产生新版本。
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum


class Gravity(str, Enum):
    """锚点吸附语义。"""

    LEFT = "left"
    RIGHT = "right"


class AnchorError(ValueError):
    """锚点参数或位置非法。"""


class EditError(ValueError):
    """单个编辑参数或范围非法。"""


class OverlappingEditsError(ValueError):
    """批量编辑之间存在重叠，整批被原子拒绝。"""

    def __init__(self, message: str, pair: tuple[int, int]):
        super().__init__(message)
        self.pair = pair


@dataclass(frozen=True)
class Edit:
    """以基准版本偏移定义的一次原子编辑。

    start/end 为基准版本上的码点偏移，半开区间 [start, end)：
    - start == end 表示在该间隙纯插入；
    - new_text == "" 表示删除；
    - 二者同时存在表示替换。
    """

    start: int
    end: int
    new_text: str = ""

    @property
    def kind(self) -> str:
        if self.end > self.start:
            return "replace" if self.new_text else "delete"
        return "insert"


@dataclass(frozen=True)
class CollapseEvent:
    """锚点被删除覆盖时记录的折叠原因。"""

    edit_index: int
    kind: str
    base_start: int
    base_end: int
    inserted_length: int
    version: int
    reason: str = "deleted"

    def to_dict(self) -> dict:
        return {
            "reason": self.reason,
            "edit_index": self.edit_index,
            "edit_kind": self.kind,
            "base_start": self.base_start,
            "base_end": self.base_end,
            "inserted_length": self.inserted_length,
            "version": self.version,
        }


@dataclass(frozen=True)
class AnchorResult:
    """单个锚点的重定位结果。"""

    id: str
    position: int
    gravity: str
    version: int
    collapsed: bool
    events: tuple[CollapseEvent, ...]

    def to_dict(self) -> dict:
        return {
            "id": self.id,
            "position": self.position,
            "gravity": self.gravity,
            "version": self.version,
            "collapsed": self.collapsed,
            "events": [event.to_dict() for event in self.events],
        }


# ---------------------------------------------------------------------------
# 内部段模型
#
# 一批互不重叠的编辑把基准文本切成若干“段”（segment）：
#   kept 段：基准文本中未被修改的一段码点，一对一搬到新文本；
#   edit 段：某个编辑覆盖的区域，其新内容是 new_text 码点序列
#            （纯删除时为空）。
# 段按基准偏移升序排列。利用每段的 base_lo/base_hi 与
# new_lo/new_hi，可确定任意基准间隙在新文本中的位置。
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class _Segment:
    base_lo: int
    base_hi: int
    new_lo: int
    new_hi: int
    edit_index: int = -1  # -1 表示 kept 段
    edit_kind: str = ""
    new_text_len: int = 0


def _coerce_gravity(value: object) -> Gravity:
    if isinstance(value, Gravity):
        return value
    if isinstance(value, str):
        try:
            return Gravity(value)
        except ValueError:
            pass
    raise AnchorError(f"gravity 必须是 'left' 或 'right'，收到: {value!r}")


def normalize_edits(edits: object, base_length: int) -> tuple[Edit, ...]:
    """校验并排序一批编辑；发现重叠时抛出 OverlappingEditsError。

    返回的编辑按 (start, end, 原始下标) 排序。
    纯插入（零长区间）之间永不互斥；非零区间与任何相交于其内部
    码点的编辑互斥（端点相接不算重叠）。
    """

    if not isinstance(edits, (list, tuple)):
        raise EditError("edits 必须是列表")

    normalized: list[Edit] = []
    for idx, raw in enumerate(edits):
        if isinstance(raw, Edit):
            edit = raw
        elif isinstance(raw, dict):
            try:
                edit = Edit(
                    start=raw["start"],
                    end=raw.get("end", raw["start"]),
                    new_text=raw.get("new_text", ""),
                )
            except KeyError as exc:
                raise EditError(f"第 {idx} 个编辑缺少字段: {exc.args[0]}") from None
        else:
            raise EditError(f"第 {idx} 个编辑必须是 Edit 或字典")

        if isinstance(edit.start, bool) or not isinstance(edit.start, int):
            raise EditError(f"第 {idx} 个编辑的 start 必须是整数")
        if isinstance(edit.end, bool) or not isinstance(edit.end, int):
            raise EditError(f"第 {idx} 个编辑的 end 必须是整数")
        if not isinstance(edit.new_text, str):
            raise EditError(f"第 {idx} 个编辑的 new_text 必须是字符串")
        if edit.start < 0 or edit.end < edit.start or edit.end > base_length:
            raise EditError(
                f"第 {idx} 个编辑范围非法: [{edit.start}, {edit.end})，"
                f"基准长度 {base_length}"
            )
        normalized.append(edit)

    # 稳定排序：同起点时零长插入排在删除/替换之前。
    order = sorted(
        range(len(normalized)),
        key=lambda i: (normalized[i].start, normalized[i].end, i),
    )
    sorted_edits = tuple(normalized[i] for i in order)
    sorted_orig = order

    # 扫描线式重叠检测。active 为 (end, start, 原始下标)。
    active: list[tuple[int, int, int]] = []
    for rank, edit in enumerate(sorted_edits):
        orig = sorted_orig[rank]
        active = [item for item in active if item[0] > edit.start]
        if edit.end > edit.start and active:
            other = active[0][2]
            lo, hi = sorted((orig, other))
            raise OverlappingEditsError(
                f"第 {lo} 与第 {hi} 个编辑重叠（编辑 {orig} 区间 "
                f"[{edit.start}, {edit.end}) 与活动编辑相交）",
                (lo, hi),
            )
        active.append((edit.end, edit.start, orig))

    return sorted_edits


def _build_segments(edits: tuple[Edit, ...], base_length: int) -> tuple[_Segment, ...]:
    segments: list[_Segment] = []
    cursor_base = 0
    cursor_new = 0
    for index, edit in enumerate(edits):
        if edit.start > cursor_base:
            kept_len = edit.start - cursor_base
            segments.append(
                _Segment(
                    cursor_base,
                    edit.start,
                    cursor_new,
                    cursor_new + kept_len,
                )
            )
            cursor_new += kept_len
        insertion_len = len(edit.new_text)
        segments.append(
            _Segment(
                edit.start,
                edit.end,
                cursor_new,
                cursor_new + insertion_len,
                edit_index=index,
                edit_kind=edit.kind,
                new_text_len=insertion_len,
            )
        )
        cursor_new += insertion_len
        cursor_base = edit.end
    if cursor_base < base_length:
        kept_len = base_length - cursor_base
        segments.append(
            _Segment(
                cursor_base,
                base_length,
                cursor_new,
                cursor_new + kept_len,
            )
        )
    return tuple(segments)


def _apply_sorted(
    base_text: str, edits: tuple[Edit, ...]
) -> tuple[str, tuple[_Segment, ...]]:
    parts: list[str] = []
    cursor = 0
    for edit in edits:
        parts.append(base_text[cursor : edit.start])
        parts.append(edit.new_text)
        cursor = edit.end
    parts.append(base_text[cursor:])
    new_text = "".join(parts)
    return new_text, _build_segments(edits, len(base_text))


def _locate_in_segments(
    position: int,
    gravity: Gravity,
    segments: tuple[_Segment, ...],
    base_length: int,
) -> tuple[int, _Segment | None]:
    """返回 (新位置, 锚点所依附的编辑段或 None)。

    判定依据是基准码点而非插入段：
    - LEFT 依附于位置左侧的码点 position-1（含于某非零长段）；
    - RIGHT 依附于位置右侧的码点 position（含于某非零长段）。
    零长纯插入段不包含基准码点，其位移由相邻段的 new_lo/new_hi
    邻接关系自动体现，从而得到“左吸附留在插入内容之前、
    右吸附越过插入内容”的语义。
    """

    if isinstance(position, bool) or not isinstance(position, int):
        raise AnchorError("锚点位置必须是整数")
    if not 0 <= position <= base_length:
        raise AnchorError(
            f"锚点位置 {position} 越界，合法范围 0..{base_length}"
        )
    if not segments:
        return 0, None

    if gravity is Gravity.LEFT:
        if position == 0:
            return segments[0].new_lo, None
        host = next(
            segment
            for segment in segments
            if segment.base_hi > segment.base_lo
            and segment.base_lo < position <= segment.base_hi
        )
        if host.edit_index >= 0:
            # 左吸附锚点在替换/删除区间内（含右端点）：折叠到插入起点。
            return host.new_lo, host
        return host.new_lo + (position - host.base_lo), None

    if position == base_length:
        return segments[-1].new_hi, None
    host = next(
        segment
        for segment in segments
        if segment.base_hi > segment.base_lo
        and segment.base_lo <= position < segment.base_hi
    )
    if host.edit_index >= 0:
        # 右吸附锚点在替换/删除区间内（含左端点）：折叠到插入终点。
        return host.new_hi, host
    return host.new_lo + (position - host.base_lo), None


# ---------------------------------------------------------------------------
# 版本历史
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class _CommittedChange:
    edits: tuple[Edit, ...]
    segments: tuple[_Segment, ...]
    old_length: int


class TextHistory:
    """保存一段文本的版本链，并负责跨版本映射锚点。"""

    def __init__(self, initial_text: str = ""):
        if not isinstance(initial_text, str):
            raise AnchorError("initial_text 必须是字符串")
        self._texts: list[str] = [initial_text]
        self._changes: list[_CommittedChange] = []

    @property
    def text(self) -> str:
        return self._texts[-1]

    @property
    def version(self) -> int:
        return len(self._texts) - 1

    def apply(self, edits: object) -> int:
        """原子应用一批基准编辑，返回新版本号。

        任何一个编辑非法、或编辑之间重叠，都会抛出异常，
        当前版本保持不变。
        """

        base_text = self.text
        normalized = normalize_edits(edits, len(base_text))
        new_text, segments = _apply_sorted(base_text, normalized)
        self._changes.append(
            _CommittedChange(normalized, segments, len(base_text))
        )
        self._texts.append(new_text)
        return self.version

    def locate(
        self,
        position: int,
        gravity: Gravity | str,
        version: int = 0,
        anchor_id: str | None = None,
    ) -> AnchorResult:
        """把 version 版本上的锚点逐版本映射到当前版本。"""

        grav = _coerce_gravity(gravity)
        if isinstance(version, bool) or not isinstance(version, int):
            raise AnchorError("version 必须是非负整数")
        if version < 0 or version > self.version:
            raise AnchorError(
                f"version={version} 越界，当前版本为 {self.version}"
            )

        if isinstance(position, bool) or not isinstance(position, int):
            raise AnchorError("锚点位置必须是整数")
        if not 0 <= position <= len(self._texts[version]):
            raise AnchorError(
                f"锚点位置 {position} 越界，版本 {version} 合法范围 0.."
                f"{len(self._texts[version])}"
            )

        events: list[CollapseEvent] = []
        current_position = position
        for change_index in range(version, self.version):
            change = self._changes[change_index]
            new_version = change_index + 1
            new_position, segment = _locate_in_segments(
                current_position, grav, change.segments, change.old_length
            )
            if segment is not None and segment.edit_index >= 0:
                edit = change.edits[segment.edit_index]
                covered = (
                    (grav is Gravity.LEFT and edit.start < current_position <= edit.end)
                    or (
                        grav is Gravity.RIGHT
                        and edit.start <= current_position < edit.end
                    )
                )
                if covered:
                    events.append(
                        CollapseEvent(
                            edit_index=segment.edit_index,
                            kind=segment.edit_kind,
                            base_start=edit.start,
                            base_end=edit.end,
                            inserted_length=segment.new_text_len,
                            version=new_version,
                        )
                    )
            current_position = new_position

        return AnchorResult(
            id="" if anchor_id is None else str(anchor_id),
            position=current_position,
            gravity=grav.value,
            version=self.version,
            collapsed=bool(events),
            events=tuple(events),
        )


def apply_edits(base_text: str, edits: object) -> str:
    """便捷函数：对一批互不重叠的基准编辑求结果文本。"""

    normalized = normalize_edits(edits, len(base_text))
    new_text, _ = _apply_sorted(base_text, normalized)
    return new_text
