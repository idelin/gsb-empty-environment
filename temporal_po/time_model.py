"""精确的时间表示：区分墙钟时间、时间点与持续时长。

所有量都以整数 *tick* 存储，禁止使用 float，避免按区间中点制造虚假精度。
单位通过“合成偏移转换表” :class:`UnitTable` 精确换算：

    ticks = value * ticks_per_unit + offset_ticks

乘数与偏移均为整数，因此换算可逆、可核验。
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timezone


@dataclass(frozen=True)
class UnitEntry:
    """合成转换表中的一行。"""

    name: str
    ticks_per_unit: int
    offset_ticks: int = 0

    def __post_init__(self) -> None:
        if self.ticks_per_unit <= 0:
            raise ValueError(f"单位 {self.name!r} 的每单位 tick 数必须为正整数")


class UnitTable:
    """合成偏移转换表：单位值 <-> 整数 tick。"""

    def __init__(self, entries: dict[str, UnitEntry], base_unit: str = "tick") -> None:
        if base_unit not in entries:
            raise ValueError(f"基准单位 {base_unit!r} 不在转换表中")
        self._entries = dict(entries)
        self.base_unit = base_unit

    def to_ticks(self, value: int, unit: str) -> int:
        entry = self._entry(unit)
        if not isinstance(value, int):
            raise TypeError("时间值必须是整数，禁止使用浮点/中点值")
        return value * entry.ticks_per_unit + entry.offset_ticks

    def from_ticks(self, ticks: int, unit: str) -> int:
        entry = self._entry(unit)
        shifted = ticks - entry.offset_ticks
        if shifted % entry.ticks_per_unit != 0:
            raise ValueError(
                f"{ticks} ticks 无法用单位 {unit!r} 精确表示（整除性不成立）"
            )
        return shifted // entry.ticks_per_unit

    def duration_units(self) -> list[UnitEntry]:
        """可用于时长格式化的单位（偏移量非零的合成单位只适用于时间点）。"""
        return sorted(
            (e for e in self._entries.values() if e.offset_ticks == 0),
            key=lambda e: -e.ticks_per_unit,
        )

    def _entry(self, unit: str) -> UnitEntry:
        try:
            return self._entries[unit]
        except KeyError as exc:
            raise KeyError(f"未知时间单位 {unit!r}") from exc

    @staticmethod
    def metric() -> "UnitTable":
        """标准表：tick=秒；另含一个合成单位 ``beat``（每拍 30 秒，偏移 90 秒）。"""
        return UnitTable(
            {
                "tick": UnitEntry("tick", 1),
                "second": UnitEntry("second", 1),
                "minute": UnitEntry("minute", 60),
                "hour": UnitEntry("hour", 3600),
                "day": UnitEntry("day", 86400),
                "beat": UnitEntry("beat", 30, offset_ticks=90),
            },
            base_unit="tick",
        )


@dataclass(frozen=True, order=True)
class Duration:
    """持续时长：两个时间点之差（可正可负），单位是整数 tick。"""

    ticks: int

    def __add__(self, other: "Duration") -> "Duration":
        return Duration(self.ticks + other.ticks)

    def __sub__(self, other: "Duration") -> "Duration":
        return Duration(self.ticks - other.ticks)

    def __neg__(self) -> "Duration":
        return Duration(-self.ticks)

    def positive(self) -> bool:
        return self.ticks > 0

    def format(self, table: UnitTable | None = None) -> str:
        table = table or UnitTable.metric()
        sign = "-" if self.ticks < 0 else ""
        rest = abs(self.ticks)
        parts: list[str] = []
        for entry in table.duration_units():
            if entry.name == table.base_unit:
                continue
            if rest >= entry.ticks_per_unit:
                value, rest = divmod(rest, entry.ticks_per_unit)
                short = entry.name[0]
                parts.append(f"{value}{short}")
        if rest or not parts:
            parts.append(f"{rest}{table.base_unit}")
        return sign + "".join(parts)


@dataclass(frozen=True)
class TimePoint:
    """时间线上的一个整数刻度点。这是赋时枚举中事件的取值。"""

    ticks: int

    def __sub__(self, other: "TimePoint") -> Duration:
        return Duration(self.ticks - other.ticks)

    def __add__(self, delta: Duration) -> "TimePoint":
        if not isinstance(delta, Duration):
            raise TypeError(
                "时间点只能与持续时长相加；两个时间点不能相加"
            )
        return TimePoint(self.ticks + delta.ticks)

    def __lt__(self, other: "TimePoint") -> bool:
        return self.ticks < other.ticks

    def __le__(self, other: "TimePoint") -> bool:
        return self.ticks <= other.ticks


@dataclass(frozen=True)
class WallClock:
    """墙钟时间：时间点在给定纪元与时区下的日历表示。"""

    when: datetime

    @staticmethod
    def from_timepoint(
        point: TimePoint, epoch: "WallClock", tz=timezone.utc
    ) -> "WallClock":
        base = epoch.when.timestamp()
        if float(int(base)) != base:
            raise ValueError("纪元必须落在整数 tick 上")
        return WallClock(datetime.fromtimestamp(int(base) + point.ticks, tz=tz))

    def __str__(self) -> str:
        return self.when.isoformat()
