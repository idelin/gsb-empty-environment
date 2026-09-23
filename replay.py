"""Replayable end-to-end sample. Run: python3.11 replay.py

Deterministic scenario exercising every feature: Pareto queries, transfer
times, cross-day trips, atomic reservation, rollback, generational tokens
across a simulated restart, and cancellation-driven replanning.
"""
from transit import CapacityError, CONFIRMED, REPLAN, Network, Planner, Stop, Trip


def flat(station, t):
    return Stop(station, t, t)


def show(journeys):
    if not journeys:
        return ["    <no journey>"]
    return [f"    {j!r}" for j in journeys]


def main():
    log = print
    log("== 1. network: vertices (incl. isolated ISO) and parallel edges ==")
    net = Network()
    net.add_vertex("A", transfer_time=0)
    net.add_vertex("HUB", transfer_time=2)
    net.add_vertex("C", transfer_time=1)
    net.add_vertex("D", transfer_time=0)
    net.add_vertex("ISO")  # isolated vertex, preserved
    net.add_edge("walk-hub-c-slow", "HUB", "C", 6)
    net.add_edge("walk-hub-c-fast", "HUB", "C", 4)  # multiedge
    log(f"    vertices={sorted(net.vertices)} edges={sorted(net.edges)}")

    planner = Planner(net, ttl=30)
    planner.add_trip(Trip("T1", (flat("A", 480), flat("HUB", 485)), capacity=1))
    planner.add_trip(Trip("T2", (flat("HUB", 486), flat("D", 500)), capacity=1))
    planner.add_trip(Trip("T3", (flat("HUB", 487), flat("D", 505)), capacity=1))
    planner.add_trip(Trip("T4", (flat("A", 480), flat("D", 510)), capacity=1))
    planner.add_trip(Trip("T5", (flat("D", 1430), flat("C", 1510)), capacity=1))

    log("== 2. query A->D @480: T2 missed (485+2>486), T3 caught (487>=487) ==")
    journeys = planner.query("A", "D", 480)
    for line in show(journeys):
        log(line)
    assert {(j.arrival_time, j.num_transfers) for j in journeys} == {(505, 1), (510, 0)}

    log("== 3. confirm the 1-transfer journey (last seat on T1 and T3) ==")
    via_hub = next(j for j in journeys if j.num_transfers == 1)
    cj = planner.confirm(via_hub, now=480)
    log(f"    {cj.id} status={cj.status} token=({cj.reservation_id}, gen {cj.generation})")
    log(f"    remaining T1={planner.reservations.remaining['T1']} T3={planner.reservations.remaining['T3']}")
    assert cj.status == CONFIRMED

    log("== 4. competing request for the same last seat: rejected ==")
    try:
        planner.confirm(via_hub, now=480)
        raise AssertionError("second reservation must fail")
    except CapacityError as exc:
        log(f"    CapacityError: {exc}")

    log("== 5. partial reservation rolls back: no capacity leak ==")
    assert planner.release_confirmed(cj.id)
    hub_to_d = planner.query("HUB", "D", 487)[0]
    planner.reservations.reserve(hub_to_d, now=480)  # fills T3 only
    before = dict(planner.reservations.remaining)
    try:
        planner.reservations.reserve(via_hub, now=480)  # takes T1, fails on T3
        raise AssertionError("must fail on T3")
    except CapacityError as exc:
        log(f"    CapacityError: {exc}")
    log(f"    remaining before={before} after={planner.reservations.remaining}")
    assert planner.reservations.remaining == before

    log("== 6. restart + expiry: stale token cannot free the new holder's seat ==")
    import json
    snap = json.loads(json.dumps(planner.reservations.snapshot()))
    restored = type(planner.reservations).restore(snap, timetable=planner.timetable)
    expired = restored.expire(now=510)  # ttl 30 from t=480
    log(f"    expired={expired} remaining T3={restored.remaining['T3']}")
    assert not restored.release(expired[0], 0)  # old generation
    log(f"    stale release rejected; remaining T3={restored.remaining['T3']}")
    assert restored.remaining["T3"] == [1]
    planner.reservations.expire(now=510)  # same clock on the live system

    log("== 7. cancel T3 mid-journey: confirmed journey must be replanned ==")
    cj2 = planner.confirm(via_hub, now=480)
    log(f"    {cj2.id} status={cj2.status}")
    planner.cancel_trip("T3")
    log(f"    after cancelling T3: {cj2.id} status={cj2.status}")
    assert cj2.status == REPLAN
    log("    re-query A->D @480 (evidence invalidated, T3 gone):")
    for line in show(planner.query("A", "D", 480)):
        log(line)
    assert [(j.arrival_time, j.num_transfers) for j in planner.query("A", "D", 480)] == [(510, 0)]

    log("== 8. cross-day trip: D->C @1400 arrives past midnight ==")
    for line in show(planner.query("D", "C", 1400)):
        log(line)
    assert planner.query("D", "C", 1400)[0].arrival_time == 1510

    log("REPLAY OK")


if __name__ == "__main__":
    main()
