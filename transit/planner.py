"""已确认旅程的证据跟踪。

确认旅程时记录其依赖的 (trip_id, version) 证据；班次被取消或更正
（版本号变化）时，相关已确认旅程状态置为 REPLAN_REQUIRED，需重新规划。
"""
from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum

from .model import Timetable
from .query import Journey, QueryEngine


class JourneyStatus(str, Enum):
    CONFIRMED = "CONFIRMED"
    REPLAN_REQUIRED = "REPLAN_REQUIRED"


@dataclass
class ConfirmedJourney:
    journey: Journey
    token: str
    evidence: dict[str, int] = field(default_factory=dict)  # trip_id -> 确认时版本
    status: JourneyStatus = JourneyStatus.CONFIRMED


class Planner:
    def __init__(self, timetable: Timetable, engine: QueryEngine, reservations) -> None:
        self.tt = timetable
        self.engine = engine
        self.reservations = reservations
        self.confirmed: list[ConfirmedJourney] = []
        timetable.add_listener(self._on_trip_changed)

    def confirm(self, journey: Journey, token: str) -> ConfirmedJourney:
        evidence = {tid: self.tt.trips[tid].version for tid in journey.trip_ids}
        cj = ConfirmedJourney(journey, token, evidence)
        self.confirmed.append(cj)
        return cj

    def _on_trip_changed(self, trip_id: str) -> None:
        trip = self.tt.trips[trip_id]
        for cj in self.confirmed:
            if cj.status is not JourneyStatus.CONFIRMED:
                continue
            if trip_id in cj.evidence:
                if trip.cancelled or trip.version != cj.evidence[trip_id]:
                    cj.status = JourneyStatus.REPLAN_REQUIRED

    def replan(self, origin: str, dest: str, depart_after: int, max_transfers: int = 2):
        return self.engine.query(origin, dest, depart_after, max_transfers)

    def report(self) -> list[str]:
        lines = []
        for i, cj in enumerate(self.confirmed, 1):
            lines.append(
                f"J{i} status={cj.status.value} trips={cj.journey.trip_ids} "
                f"arrival={cj.journey.arrival}"
            )
        return lines
