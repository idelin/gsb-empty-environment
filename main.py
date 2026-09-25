"""Demo scenarios: exact-clearance grid planning among polygon obstacles.

Run:  python3.11 main.py
"""

from planner import Bounds, GridPlanner, NoPathError
from validate import validate_path


def fmt_pt(p):
    return f"({p[0]}, {p[1]})"


def fmt_frac(v):
    return f"{v} (~{float(v):.4f})"


def show_reports(title, path, obstacles, radius, bounds):
    print(f"  simplified path ({len(path)} waypoints):")
    for p in path:
        print(f"    {fmt_pt(p)}")
    reports = validate_path(path, obstacles, radius, bounds)
    print("  per-segment collision check (exact rational arithmetic):")
    for rep in reports:
        print(f"    segment {rep.index}: {fmt_pt(rep.start)} -> {fmt_pt(rep.end)}")
        for k, c in enumerate(rep.clearance2):
            mark = "OK " if c >= rep.required2 else "FAIL"
            print(
                f"      clearance^2 to obstacle {k}: {fmt_frac(c)}"
                f"  required >= {rep.required2}  [{mark}]"
            )
        if not rep.clearance2:
            print("      (no obstacles)")
        print(
            f"      within eroded bounds: {rep.within_bounds},"
            f" enters obstacle interior: {rep.enters_interior},"
            f" verdict: {'OK' if rep.ok else 'FAIL'}"
        )
    assert all(rep.ok for rep in reports), "demo path failed validation!"
    print("  => all segments collision-free with required clearance\n")


def scenario_boxes():
    print("=" * 72)
    print("Scenario 1: warehouse with two box obstacles, robot radius 1/2")
    print("=" * 72)
    bounds = Bounds.of(0, 0, 16, 10)
    obstacles = [
        [(4, 2), (7, 2), (7, 5), (4, 5)],
        [(10, 4), (13, 4), (13, 8), (10, 8)],
    ]
    planner = GridPlanner(obstacles, radius="1/2", bounds=bounds, step=1)
    plan = planner.plan((1, 1), (15, 9))
    print(f"  raw grid path: {len(plan.grid_path)} nodes")
    show_reports("boxes", plan.waypoints, obstacles, "1/2", bounds)


def scenario_diagonal_pinch():
    print("=" * 72)
    print("Scenario 2: two diagonally touching obstacles (point-contact gap)")
    print("=" * 72)
    bounds = Bounds.of(0, 0, 12, 12)
    obstacles = [
        [(0, 5), (5, 5), (5, 7), (0, 7)],   # touches left wall, corner (5,5)
        [(5, 3), (12, 3), (12, 5), (5, 5)],  # touches right wall, corner (5,5)
    ]
    start, goal = (6, 10), (6, 1)

    planner = GridPlanner(obstacles, radius="1/2", bounds=bounds, step=1)
    try:
        planner.plan(start, goal)
        print("  radius 1/2: UNEXPECTEDLY found a path!")
    except NoPathError as exc:
        print(f"  radius 1/2: no path (as required) -- {exc}")

    planner0 = GridPlanner(obstacles, radius=0, bounds=bounds, step=1)
    plan0 = planner0.plan(start, goal)
    print("  radius 0 (point robot, contact allowed): path exists")
    show_reports("pinch", plan0.waypoints, obstacles, 0, bounds)


def scenario_exact_gap():
    print("=" * 72)
    print("Scenario 3: gap of exactly 2*radius -- contact with the inflated")
    print("obstacle is allowed (clearance == radius, exact '>=' convention)")
    print("=" * 72)
    bounds = Bounds.of(0, 0, 12, 12)
    obstacles = [
        [(0, 5), (5, 5), (5, 7), (0, 7)],
        [(5, "3/2"), (12, "3/2"), (12, "7/2"), (5, "7/2")],
        # gap between y=5 (obstacle 0 bottom) and y=7/2 (obstacle 1 top)
        # is exactly 3/2 > 2*(1/2); the corridor center has clearance 1/2.
    ]
    planner = GridPlanner(obstacles, radius="1/2", bounds=bounds, step=1)
    plan = planner.plan((6, 10), (6, 1))
    show_reports("gap", plan.waypoints, obstacles, "1/2", bounds)


def scenario_start_covered():
    print("=" * 72)
    print("Scenario 4: start covered by an inflated obstacle -> no solution")
    print("=" * 72)
    bounds = Bounds.of(0, 0, 10, 10)
    obstacles = [[(2, 2), (4, 2), (4, 4), (2, 4)]]
    planner = GridPlanner(obstacles, radius="3/2", bounds=bounds, step=1)
    try:
        planner.plan((1, 3), (9, 9))  # distance 1 from box < radius 3/2
        print("  UNEXPECTEDLY found a path!")
    except NoPathError as exc:
        print(f"  no solution (as required) -- {exc}")
    print()


if __name__ == "__main__":
    scenario_boxes()
    scenario_diagonal_pinch()
    scenario_exact_gap()
    scenario_start_covered()
