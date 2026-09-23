"""Pareto journey queries over a discrete timetable.

Semantics:
- Intervals are left-closed / right-open: a trip departing at ``d`` can be
  boarded at time ``d``.
- Changing trips at a station requires the station's transfer time: after
  alighting at ``a`` the next departure must satisfy
  ``dep >= a + transfer_time``. Walking to another station and boarding
  there requires ``dep >= a + walk + transfer_time``. The first boarding
  of a query carries no transfer penalty.
- Journeys are scored on (arrival_time, num_transfers); the query returns
  one representative journey per non-dominated pair.
"""
from __future__ import annotations

import heapq
import itertools

from .model import Journey, RideLeg, WalkLeg


def pareto_query(timetable, origin: str, destination: str, start_time: int) -> list[Journey]:
    network = timetable.network
    if origin == destination:
        return []
    walks = network.shortest_walks()
    transfer = {vid: v.transfer_time for vid, v in network.vertices.items()}

    at_station: dict[str, list[tuple[int, object]]] = {}
    for trip in timetable.active_trips():
        for idx, stop in enumerate(trip.stops[:-1]):
            at_station.setdefault(stop.station, []).append((idx, trip))

    best: dict[str, list[tuple[int, int, int]]] = {}
    dest_pairs: list[tuple[int, int]] = []
    results: dict[tuple[int, int], Journey] = {}
    counter = itertools.count()
    pq: list[tuple] = []

    def is_dominated(station, time, ready, transfers):
        for t, r, k in best.get(station, ()):
            if t <= time and r <= ready and k <= transfers:
                return True
        for t, k in dest_pairs:
            if t <= time and k <= transfers:
                return True
        return False

    def add_label(station, time, arrived_by_trip, transfers, legs):
        # ``ready`` is the earliest moment the next trip can be boarded here.
        ready = time if station == destination else time + (
            transfer[station] if arrived_by_trip else 0
        )
        if is_dominated(station, time, ready, transfers):
            return
        kept = [
            (t, r, k)
            for t, r, k in best.get(station, [])
            if not (time <= t and ready <= r and transfers <= k)
        ]
        kept.append((time, ready, transfers))
        best[station] = kept
        heapq.heappush(
            pq, (ready, transfers, next(counter), station, time, arrived_by_trip, legs)
        )

    add_label(origin, start_time, False, 0, ())

    while pq:
        ready, transfers, _, station, time, arrived_by_trip, legs = heapq.heappop(pq)
        if (time, ready, transfers) not in best.get(station, ()):
            continue  # stale label, since dominated by a newer one
        if station == destination:
            pair = (time, transfers)
            if pair not in results:
                results[pair] = Journey(legs)
                dest_pairs.append(pair)
            continue
        has_ride = any(isinstance(leg, RideLeg) for leg in legs)
        last_leg = legs[-1] if legs else None
        for idx, trip in at_station.get(station, ()):
            stop = trip.stops[idx]
            if stop.departure < ready:
                continue
            if isinstance(last_leg, RideLeg) and last_leg.trip_id == trip.id:
                continue  # staying on board strictly dominates re-boarding
            next_transfers = transfers + (1 if has_ride else 0)
            for j in range(idx + 1, len(trip.stops)):
                alight = trip.stops[j]
                leg = RideLeg(
                    trip.id, stop.station, stop.departure,
                    alight.station, alight.arrival,
                )
                add_label(alight.station, alight.arrival, True, next_transfers, legs + (leg,))
        for dst, walk in walks[station].items():
            if dst == station or walk is None:
                continue
            leg = WalkLeg(station, dst, time, time + walk)
            add_label(dst, time + walk, True, transfers, legs + (leg,))

    return [
        journey
        for (t, k), journey in sorted(results.items())
        if not any(
            t2 <= t and k2 <= k and (t2, k2) != (t, k) for (t2, k2) in results
        )
    ]


def pareto_filter(pairs):
    return {
        (t, k)
        for (t, k) in pairs
        if not any(t2 <= t and k2 <= k and (t2, k2) != (t, k) for (t2, k2) in pairs)
    }


def brute_force_pareto_pairs(timetable, origin: str, destination: str, start_time: int):
    """Exhaustive enumeration over an explicit time-expanded graph.

    Nodes are trip stop events plus a source node at ``origin``; edges are
    ride segments, feasible transfers (respecting per-station transfer
    times) and walking links. Every source-to-sink path is enumerated and
    the Pareto frontier of (arrival, transfers) pairs is returned. Used as
    an independent oracle for ``pareto_query``.
    """
    network = timetable.network
    if origin == destination:
        return set()
    walks = network.shortest_walks()
    transfer = {vid: v.transfer_time for vid, v in network.vertices.items()}
    trips = timetable.active_trips()

    deps_at: dict[str, list[tuple[int, int, int]]] = {}
    for ti, trip in enumerate(trips):
        for j, stop in enumerate(trip.stops[:-1]):
            deps_at.setdefault(stop.station, []).append((stop.departure, ti, j))

    pairs: set[tuple[int, int]] = set()

    def sink_arrivals(station, time, cost):
        if station == destination:
            pairs.add((time, cost))
        else:
            walk = walks[station][destination]
            if walk is not None:
                pairs.add((time + walk, cost))

    def board_from(station, earliest, cost, first, visited):
        for dep, ti, j in deps_at.get(station, ()):
            if dep < earliest or (ti, j) in visited:
                continue
            ride(ti, j, cost if first else cost + 1, visited | {(ti, j)})

    def ride(ti, j, cost, visited):
        stops = trips[ti].stops
        for i in range(j + 1, len(stops)):
            arr = stops[i]
            sink_arrivals(arr.station, arr.arrival, cost)
            board_from(arr.station, arr.arrival + transfer[arr.station], cost, False, visited)
            for dst, walk in walks[arr.station].items():
                if dst == arr.station or walk is None:
                    continue
                board_from(dst, arr.arrival + walk + transfer[dst], cost, False, visited)

    sink_arrivals(origin, start_time, 0)
    board_from(origin, start_time, 0, True, frozenset())
    for dst, walk in walks[origin].items():
        if dst == origin or walk is None:
            continue
        board_from(dst, start_time + walk + transfer[dst], 0, True, frozenset())

    return pareto_filter(pairs)
