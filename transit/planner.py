"""Facade tying timetable, query evidence and reservations together.

- Query results are cached as *evidence* tagged with the timetable
  version; any trip update (cancel/correct/add) bumps the version and
  thereby invalidates all related evidence.
- Confirmed journeys that depend on an updated trip are moved to REPLAN.
"""
from __future__ import annotations

from dataclasses import dataclass

from .model import Journey, Network, Timetable
from .query import pareto_query
from .reservation import ReservationSystem

CONFIRMED = "confirmed"
REPLAN = "replan"


@dataclass
class ConfirmedJourney:
    id: str
    journey: Journey
    reservation_id: str
    generation: int
    status: str = CONFIRMED


class Planner:
    def __init__(self, network: Network, ttl: int = 30) -> None:
        self.timetable = Timetable(network)
        self.reservations = ReservationSystem(self.timetable, ttl=ttl)
        self._evidence: dict[tuple, tuple[int, list]] = {}
        self.confirmed: dict[str, ConfirmedJourney] = {}
        self._seq = 0

    @property
    def network(self) -> Network:
        return self.timetable.network

    def add_trip(self, trip) -> None:
        self.timetable.add_trip(trip)
        self.reservations.load_trip(trip)

    def query(self, origin: str, destination: str, start_time: int):
        key = (origin, destination, start_time)
        cached = self._evidence.get(key)
        if cached is not None and cached[0] == self.timetable.version:
            return cached[1]
        result = pareto_query(self.timetable, origin, destination, start_time)
        self._evidence[key] = (self.timetable.version, result)
        return result

    def evidence_version(self, origin: str, destination: str, start_time: int):
        cached = self._evidence.get((origin, destination, start_time))
        return None if cached is None else cached[0]

    def confirm(self, journey: Journey, now: int) -> ConfirmedJourney:
        reservation = self.reservations.reserve(journey, now)
        self._seq += 1
        confirmed = ConfirmedJourney(
            id=f"J{self._seq}",
            journey=journey,
            reservation_id=reservation.id,
            generation=reservation.generation,
        )
        self.confirmed[confirmed.id] = confirmed
        return confirmed

    def release_confirmed(self, confirmed_id: str) -> bool:
        confirmed = self.confirmed[confirmed_id]
        ok = self.reservations.release(confirmed.reservation_id, confirmed.generation)
        return ok

    def cancel_trip(self, trip_id: str) -> None:
        self.timetable.cancel_trip(trip_id)
        self._flag_replan(trip_id)

    def correct_trip(self, trip_id: str, **changes) -> None:
        old = self.timetable.trips[trip_id]
        new = self.timetable.correct_trip(trip_id, **changes)
        if new.num_segments != old.num_segments or new.capacity != old.capacity:
            self.reservations.load_trip(new)  # layout changed: rebuild counters
        self._flag_replan(trip_id)

    def _flag_replan(self, trip_id: str) -> None:
        for confirmed in self.confirmed.values():
            if confirmed.status == CONFIRMED and trip_id in confirmed.journey.trip_ids():
                confirmed.status = REPLAN
