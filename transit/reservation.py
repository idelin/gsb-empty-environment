"""Atomic multi-segment capacity reservations with generational tokens.

- ``reserve`` decrements every segment a journey needs; on any failure all
  segments taken so far are restored (no capacity leak).
- Each reservation is identified by (id, generation). Releasing or
  expiring bumps the generation, so a stale token presented afterwards is
  rejected and cannot return capacity a second time.
- ``snapshot``/``restore`` persist the full state (counters, generations,
  statuses) so expired tokens stay invalid across a restart.
"""
from __future__ import annotations

from dataclasses import dataclass

ACTIVE = "active"
RELEASED = "released"
EXPIRED = "expired"


class CapacityError(Exception):
    pass


@dataclass
class Reservation:
    id: str
    generation: int
    holds: tuple  # tuple of (trip_id, segment_index)
    expires_at: int
    status: str = ACTIVE


class ReservationSystem:
    def __init__(self, timetable=None, ttl: int = 30) -> None:
        self.timetable = timetable
        self.ttl = ttl
        self.remaining: dict[str, list[int]] = {}
        self.reservations: dict[str, Reservation] = {}
        self._seq = 0
        if timetable is not None:
            for trip in timetable.trips.values():
                self.load_trip(trip)

    def load_trip(self, trip) -> None:
        self.remaining[trip.id] = [trip.capacity] * trip.num_segments

    def _segments(self, journey) -> list[tuple[str, int]]:
        if self.timetable is None:
            raise CapacityError("no timetable bound; cannot resolve journey")
        needed: list[tuple[str, int]] = []
        for leg in journey.ride_legs:
            try:
                trip = self.timetable.trips[leg.trip_id]
            except KeyError as exc:
                raise CapacityError(f"unknown trip {leg.trip_id!r}") from exc
            board = alight = None
            for i, stop in enumerate(trip.stops):
                if stop.station == leg.board_station and stop.departure == leg.board_time:
                    board = i
                if stop.station == leg.alight_station and stop.arrival == leg.alight_time:
                    alight = i
            if board is None or alight is None or alight <= board:
                raise CapacityError(f"journey does not match trip {trip.id!r}")
            needed.extend((trip.id, seg) for seg in range(board, alight))
        return needed

    def reserve(self, journey, now: int, ttl: int | None = None) -> Reservation:
        needed = self._segments(journey)
        taken: list[tuple[str, int]] = []
        try:
            for trip_id, seg in needed:
                if self.remaining[trip_id][seg] < 1:
                    raise CapacityError(f"segment full: {trip_id}[{seg}]")
                self.remaining[trip_id][seg] -= 1
                taken.append((trip_id, seg))
        except (CapacityError, KeyError, IndexError) as exc:
            for trip_id, seg in taken:  # roll back everything taken so far
                self.remaining[trip_id][seg] += 1
            if isinstance(exc, CapacityError):
                raise
            raise CapacityError(f"unknown trip/segment: {exc}") from exc
        self._seq += 1
        reservation = Reservation(
            id=f"R{self._seq}",
            generation=0,
            holds=tuple(sorted(set(needed))),
            expires_at=now + (self.ttl if ttl is None else ttl),
        )
        self.reservations[reservation.id] = reservation
        return reservation

    def _restore(self, reservation: Reservation) -> None:
        for trip_id, seg in reservation.holds:
            self.remaining[trip_id][seg] += 1

    def release(self, reservation_id: str, generation: int) -> bool:
        """Release capacity iff the token (id, generation) is current."""
        reservation = self.reservations.get(reservation_id)
        if (
            reservation is None
            or reservation.status != ACTIVE
            or reservation.generation != generation
        ):
            return False  # stale token: no capacity change
        self._restore(reservation)
        reservation.status = RELEASED
        reservation.generation += 1
        return True

    def expire(self, now: int) -> list[str]:
        """Expire reservations whose [created, expires_at) window has ended."""
        expired = []
        for reservation in self.reservations.values():
            if reservation.status == ACTIVE and reservation.expires_at <= now:
                self._restore(reservation)
                reservation.status = EXPIRED
                reservation.generation += 1  # invalidate outstanding tokens
                expired.append(reservation.id)
        return expired

    def snapshot(self) -> dict:
        return {
            "ttl": self.ttl,
            "seq": self._seq,
            "remaining": {k: list(v) for k, v in self.remaining.items()},
            "reservations": [
                {
                    "id": r.id,
                    "generation": r.generation,
                    "holds": [list(h) for h in r.holds],
                    "expires_at": r.expires_at,
                    "status": r.status,
                }
                for r in self.reservations.values()
            ],
        }

    @classmethod
    def restore(cls, snap: dict, timetable=None) -> "ReservationSystem":
        system = cls(timetable=timetable, ttl=snap["ttl"])
        system.remaining = {k: list(v) for k, v in snap["remaining"].items()}
        system._seq = snap["seq"]
        for data in snap["reservations"]:
            reservation = Reservation(
                id=data["id"],
                generation=data["generation"],
                holds=tuple(tuple(h) for h in data["holds"]),
                expires_at=data["expires_at"],
                status=data["status"],
            )
            system.reservations[reservation.id] = reservation
        return system
