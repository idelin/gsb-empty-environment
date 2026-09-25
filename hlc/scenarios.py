"""Replayable scenario scripts.

An operation log is plain data (no live objects), so the same scenario can be
replayed repeatedly into fresh directories and compared deterministically.
The canonical :data:`MIXED_CLOCK_SCENARIO` exercises, in one trace:

* same-millisecond bursts with strict logical increments;
* a wall-clock backward step modelled by a synthetic offset-table change;
* receiving a leading remote tag and overtaking it;
* logical-counter overflow (small bounded counter -> explicit physical leap);
* a far-future hostile label rejected into quarantine;
* crashes before sync, after sync and after publish, followed by restarts
  whose wall clock reads *behind* the durable high-water mark;
* duplicate message delivery, including republish after restart.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Optional

from .persistence import CrashPoint
from .time_model import OffsetTable, WallReading


@dataclass(frozen=True)
class Op:
    action: str  # local|send|deliver|advance|install_table|restart|duplicate|inject_future
    node: str
    target: Optional[str] = None
    msg_id: Optional[str] = None
    payload: str = ""
    raw_delta: int = 0
    table: Optional[OffsetTable] = None
    start: Optional[WallReading] = None
    crash: Optional[CrashPoint] = None
    remote_epoch: int = 0
    remote_logical: int = 0
    future_micros: int = 0


@dataclass(frozen=True)
class Scenario:
    name: str
    max_logical: int
    allowed_skew_micros: int
    starts: dict[str, WallReading]
    tables: dict[str, OffsetTable]
    ops: tuple[Op, ...] = field(default_factory=tuple)


def _table_basic() -> OffsetTable:
    return OffsetTable.create(default_offset_micros=0)


MIXED_CLOCK_SCENARIO = Scenario(
    name="mixed_clock",
    max_logical=7,
    allowed_skew_micros=500_000,
    starts={
        "a": WallReading(1_000_000),
        "b": WallReading(1_000_000),
        "c": WallReading(1_000_000),
    },
    tables={"a": _table_basic(), "b": _table_basic(), "c": _table_basic()},
    ops=(
        # --- same-millisecond bursts: strict logical ordering ---
        Op("local", "a"),
        Op("local", "a"),
        Op("local", "b"),
        Op("send", "a", target="b", msg_id="m1", payload="hello"),
        Op("deliver", "b"),
        Op("local", "b"),
        Op("local", "b"),
        # node c: eight events on the same millisecond; bounded counter
        # forces an explicit physical-tick overflow leap.
        *(Op("local", "c") for _ in range(8)),
        Op("send", "c", target="b", msg_id="m2"),
        Op("deliver", "b"),
        # --- physical clocks advance; node a steps its wall clock BACK ---
        Op("advance", "a", raw_delta=1_000_000),
        Op(
            "install_table",
            "a",
            table=OffsetTable.create(default_offset_micros=-2_000_000),
        ),
        Op("local", "a"),
        Op("send", "a", target="c", msg_id="m3", payload="after-rollback"),
        Op("deliver", "c"),
        # --- leading remote tag from node c is overtaken by node a ---
        Op("advance", "c", raw_delta=3_000),
        Op("send", "c", target="a", msg_id="m4"),
        Op("deliver", "a"),
        Op("local", "a"),
        # --- far-future hostile label: isolated, local clock unaffected ---
        Op(
            "inject_future",
            "b",
            target="b",
            msg_id="evil",
            future_micros=10_000_000_000,
        ),
        Op("deliver", "b"),
        Op("local", "b"),
        # --- crash BEFORE the durable sync; nothing survives ---
        Op(
            "send",
            "c",
            target="a",
            msg_id="lost-before-sync",
            crash=CrashPoint.BEFORE_SYNC,
        ),
        Op(
            "restart",
            "c",
            start=WallReading(998_000),
            table=OffsetTable.create(default_offset_micros=0),
        ),
        # --- crash AFTER sync but BEFORE publish; restart republishes ---
        Op(
            "send",
            "c",
            target="a",
            msg_id="m5",
            payload="durable-unpublished",
            crash=CrashPoint.AFTER_SYNC,
        ),
        Op(
            "restart",
            "c",
            start=WallReading(997_000),
            table=OffsetTable.create(default_offset_micros=0),
        ),
        Op("deliver", "a"),
        # --- crash AFTER publish: peer dedupes the redelivery ---
        Op(
            "send",
            "a",
            target="b",
            msg_id="m6",
            payload="published-then-crash",
            crash=CrashPoint.AFTER_PUBLISH,
        ),
        Op(
            "restart",
            "a",
            start=WallReading(996_000),
            table=OffsetTable.create(default_offset_micros=-2_000_000),
        ),
        Op("deliver", "b"),
        Op("duplicate", "b"),
        Op("deliver", "b"),
        # post-restart node a is still strictly monotonic behind wall clock
        Op("local", "a"),
    ),
)
