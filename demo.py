"""Runnable demo: solve, verify, tamper, re-verify."""

from __future__ import annotations

from csp import CSP, Constraint, check_solution
from checker import check_trace
from solver import Solver
from trace import Event


def neq(a: str, b: str, values) -> Constraint:
    return Constraint((a, b), frozenset(
        (x, y) for x in values for y in values if x != y))


def map_coloring() -> CSP:
    colors = ("R", "G", "B")
    domains = {v: colors for v in ("WA", "NT", "SA", "Q")}
    edges = [("WA", "NT"), ("WA", "SA"), ("NT", "SA"), ("NT", "Q"), ("SA", "Q")]
    return CSP(domains, [neq(a, b, colors) for a, b in edges])


def triangle_two_coloring() -> CSP:
    vals = (1, 2)
    domains = {v: vals for v in ("A", "B", "C")}
    edges = [("A", "B"), ("B", "C"), ("A", "C")]
    return CSP(domains, [neq(a, b, vals) for a, b in edges])


def show(title: str) -> None:
    print(f"\n=== {title} ===")


def main() -> None:
    show("1. SAT instance: 3-coloring a small map")
    csp = map_coloring()
    result = Solver(csp).solve()
    for ev in result.trace:
        print(f"  {ev}")
    chk = check_trace(csp, result.trace)
    print(f"  solver says : {result.status}  assignment={result.assignment}")
    print(f"  trace check : ok={chk.ok} certified={chk.status} ({chk.message})")
    ok, msg = check_solution(csp, result.assignment)
    print(f"  independent solution check: ok={ok} ({msg})")

    show("2. UNSAT instance: 2-coloring a triangle")
    csp2 = triangle_two_coloring()
    result2 = Solver(csp2).solve()
    for ev in result2.trace:
        print(f"  {ev}")
    chk2 = check_trace(csp2, result2.trace)
    print(f"  solver says : {result2.status}")
    print(f"  trace check : ok={chk2.ok} certified={chk2.status} ({chk2.message})")

    show("3. Tampered trace: first illegal event is located")
    bad = list(result.trace)
    target = next(ev for ev in bad if ev.kind == "propagate")
    i = target.seq
    other = next(v for v in csp.domains[target.payload["var"]]
                 if v != target.payload["value"])
    bad[i] = Event("propagate", i, {**target.payload, "value": other})
    chk3 = check_trace(csp, bad)
    print(f"  tampered event #{i}: {bad[i]}")
    print(f"  trace check : ok={chk3.ok} first_bad={chk3.first_bad}")
    print(f"  reason      : {chk3.message}")

    show("4. Budget-limited search: UNKNOWN, never wrapped as UNSAT")
    result4 = Solver(csp, max_events=3).solve()
    print(f"  solver says : {result4.status} "
          f"(last event: {result4.trace[-1]})")
    forged = list(result4.trace)
    forged[-1] = Event("unsat", forged[-1].seq, {})
    chk4 = check_trace(csp, forged)
    print(f"  forged 'unsat' trace: ok={chk4.ok} first_bad={chk4.first_bad}")
    print(f"  reason              : {chk4.message}")


if __name__ == "__main__":
    main()
