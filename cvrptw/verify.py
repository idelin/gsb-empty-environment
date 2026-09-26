"""Independent route verification: replays every leg and re-checks arrival
times, waiting, time windows, per-leg load and capacity, depot horizon and
exactly-once coverage.  Used both by the demo output and the tests.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction

from .model import Problem


@dataclass(frozen=True)
class Leg:
    seq: int
    origin: int
    dest: int
    depart: Fraction
    travel: Fraction
    arrive: Fraction
    wait: Fraction
    service_start: Fraction
    load_on_arrival: Fraction   # load carried into `dest`
    load_after: Fraction        # load after serving `dest` (unchanged at depot)


@dataclass(frozen=True)
class RouteReport:
    vehicle: int
    customers: tuple
    legs: tuple
    distance: Fraction
    max_load: Fraction
    violations: tuple


@dataclass(frozen=True)
class Verification:
    ok: bool
    total_distance: Fraction
    routes: tuple
    violations: tuple


def verify_routes(problem: Problem, routes) -> Verification:
    """Replay ``routes`` (iterables of 0-based customer indices) against the
    problem and return a full per-leg report plus every violation found."""
    violations = []
    reports = []

    if len(routes) > problem.n_vehicles:
        violations.append(f"{len(routes)} routes exceed {problem.n_vehicles} vehicles")

    visits = {}
    for r, route in enumerate(routes):
        for c in route:
            if not 0 <= c < len(problem.customers):
                violations.append(f"route {r}: unknown customer index {c}")
                continue
            visits.setdefault(c, []).append(r)
    for c, cust in enumerate(problem.customers):
        seen = visits.get(c, [])
        if not seen:
            violations.append(f"customer {cust.name} never visited")
        elif len(seen) > 1:
            violations.append(f"customer {cust.name} visited {len(seen)} times (routes {seen})")

    total = Fraction(0)
    for r, route in enumerate(routes):
        legs = []
        route_violations = []
        node = 0
        depart = Fraction(0)
        load = Fraction(0)
        distance = Fraction(0)
        max_load = Fraction(0)
        seq = 0
        for c in tuple(route) + (None,):
            dest = 0 if c is None else c + 1
            travel = problem.distances[node][dest]
            arrive = depart + travel
            distance += travel
            if c is None:
                legs.append(Leg(seq, node, 0, depart, travel, arrive,
                                Fraction(0), arrive, load, load))
                if arrive > problem.horizon:
                    route_violations.append(
                        f"vehicle {r}: returns to depot at {arrive}, past horizon {problem.horizon}")
                break
            cust = problem.customers[c]
            load_on_arrival = load + cust.demand
            max_load = max(max_load, load_on_arrival)
            if load_on_arrival > problem.capacity:
                route_violations.append(
                    f"vehicle {r}: load {load_on_arrival} exceeds capacity {problem.capacity} "
                    f"at {cust.name}")
            start = max(arrive, cust.ready)
            wait = start - arrive
            if start > cust.due:
                route_violations.append(
                    f"vehicle {r}: service at {cust.name} starts {start}, past due {cust.due}")
            legs.append(Leg(seq, node, dest, depart, travel, arrive, wait, start,
                            load_on_arrival, load_on_arrival))
            load = load_on_arrival
            depart = start + cust.service
            node = dest
            seq += 1
        reports.append(RouteReport(r, tuple(route), tuple(legs), distance,
                                   max_load, tuple(route_violations)))
        violations.extend(route_violations)
        total += distance

    return Verification(not violations, total, tuple(reports), tuple(violations))


def format_verification(problem: Problem, ver: Verification) -> str:
    """Human-readable per-leg arrival / load audit table."""

    def fmt(v):
        return str(v)

    lines = []
    for rep in ver.routes:
        path = " -> ".join([problem.node_name(0)]
                           + [problem.customers[c].name for c in rep.customers]
                           + [problem.node_name(0)])
        lines.append(f"vehicle {rep.vehicle}: {path}")
        lines.append(f"  route distance={fmt(rep.distance)}  max load={fmt(rep.max_load)}"
                     f"/{fmt(problem.capacity)}")
        header = f"  {'leg':>3} {'from':<7} {'to':<7} {'depart':>7} {'travel':>7} " \
                 f"{'arrive':>7} {'wait':>6} {'serve':>7} {'load':>7}"
        lines.append(header)
        for leg in rep.legs:
            lines.append(
                f"  {leg.seq:>3} {problem.node_name(leg.origin):<7} "
                f"{problem.node_name(leg.dest):<7} {fmt(leg.depart):>7} "
                f"{fmt(leg.travel):>7} {fmt(leg.arrive):>7} {fmt(leg.wait):>6} "
                f"{fmt(leg.service_start):>7} {fmt(leg.load_on_arrival):>7}")
        for v in rep.violations:
            lines.append(f"  VIOLATION: {v}")
    lines.append(f"total distance = {fmt(ver.total_distance)}")
    lines.append("verification: " + ("OK" if ver.ok else "FAILED"))
    for v in ver.violations:
        lines.append(f"  VIOLATION: {v}")
    return "\n".join(lines)


def route_distance_if_feasible(problem: Problem, route):
    """Return the exact travel distance of ``route`` (depot -> ... -> depot)
    if it satisfies capacity, time windows and horizon, else ``None``."""
    node = 0
    depart = Fraction(0)
    load = Fraction(0)
    distance = Fraction(0)
    for c in route:
        cust = problem.customers[c]
        load += cust.demand
        if load > problem.capacity:
            return None
        travel = problem.distances[node][c + 1]
        start = max(depart + travel, cust.ready)
        if start > cust.due:
            return None
        distance += travel
        depart = start + cust.service
        node = c + 1
    back = problem.distances[node][0]
    if depart + back > problem.horizon:
        return None
    return distance + back
