"""Demo: solve N-queens and 3-coloring of K4 with a fully audited trail,
then independently replay the trail and verify the verdict."""
from csp_audit import Constraint, Problem, Solver, Status, check_sat, replay


def queens(n: int) -> Problem:
    variables = [f"q{r}" for r in range(n)]  # q_r = column of queen in row r
    domains = {v: set(range(n)) for v in variables}
    constraints = []
    for r1 in range(n):
        for r2 in range(r1 + 1, n):
            gap = r2 - r1
            constraints.append(Constraint(
                f"q{r1}_q{r2}", (f"q{r1}", f"q{r2}"),
                lambda t, gap=gap: t[0] != t[1] and abs(t[0] - t[1]) != gap))
    return Problem(variables, domains, constraints)


def k_coloring(edges, colors) -> Problem:
    nodes = sorted({u for e in edges for u in e})
    domains = {f"x{n}": set(colors) for n in nodes}
    constraints = [
        Constraint(f"e{u}_{v}", (f"x{u}", f"x{v}"), lambda t: t[0] != t[1])
        for u, v in edges
    ]
    return Problem([f"x{n}" for n in nodes], domains, constraints)


def run(name, problem, max_events=None):
    print(f"=== {name} ===")
    result = Solver(problem, max_events=max_events).solve()
    print(f"status: {result.status.value}, trail events: {len(result.trail)}")
    if result.status is Status.SAT:
        print(f"assignment: {result.assignment}")
        print(f"independent check_sat: {check_sat(problem, result.assignment)}")
    rep = replay(problem, result.trail, result.status.value, result.assignment)
    print(f"replay: ok={rep.ok} verdict_supported={rep.verdict_supported} ({rep.message})")
    print("first events:")
    for i, ev in enumerate(result.trail):
        if i >= 8:
            print(f"  ... ({len(result.trail) - 8} more)")
            break
        print(f"  [{i}] {ev}")
    print()
    return result


if __name__ == "__main__":
    run("4-queens (SAT)", queens(4))
    run("K4 3-coloring (UNSAT)", k_coloring(
        [(1, 2), (1, 3), (1, 4), (2, 3), (2, 4), (3, 4)], colors={1, 2, 3}))
    run("8-queens with max_events=3 (UNKNOWN, not UNSAT)", queens(8), max_events=3)
