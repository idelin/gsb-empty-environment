"""Runnable demo: integer and rational instances, plus budget-limited runs.

Run with:  python3.11 example.py
"""

from fractions import Fraction

from scheduler import Job, constant_setup, matrix_setup, solve


def show(title, result):
    print(f"== {title} ==")
    print(
        f"status={result.status.value}  nodes={result.nodes_explored}  "
        f"budget_exhausted={result.budget_exhausted}"
    )
    if result.schedule is None:
        print("no complete schedule found within the budget")
    else:
        print(
            f"total_tardiness={result.total_tardiness}  "
            f"certified_lower_bound={result.certified_lower_bound}"
        )
        print(f"{'job':>4} {'type':>5} {'start':>6} {'end':>6} {'tardy':>6}")
        for entry in result.schedule:
            print(
                f"{str(entry.job.job_id):>4} {str(entry.job.job_type):>5} "
                f"{str(entry.start):>6} {str(entry.completion):>6} "
                f"{str(entry.tardiness):>6}"
            )
    print()


def main():
    # 1) Integer instance, two types, constant switch cost of 2.
    jobs = [
        Job("J1", 0, 3, 10, "A"),
        Job("J2", 1, 2, 6, "B"),
        Job("J3", 2, 4, 14, "A"),
        Job("J4", 0, 1, 4, "B"),
        Job("J5", 5, 2, 9, "A"),
        Job("J6", 3, 3, 16, "B"),
        Job("J7", 6, 1, 8, "A"),
        Job("J8", 4, 2, 12, "B"),
    ]
    setup = constant_setup(2)
    show("integer instance, unlimited budget", solve(jobs, setup))

    # 2) Same instance, small node budget: best feasible schedule, no proof.
    show("integer instance, node_budget=30", solve(jobs, setup, node_budget=30))

    # 3) Same instance, zero budget: undecided, no schedule at all.
    show("integer instance, node_budget=0", solve(jobs, setup, node_budget=0))

    # 4) Rational data (fractions.Fraction), asymmetric setup matrix.
    rational_jobs = [
        Job("R1", 0, Fraction(3, 2), 4, "alpha"),
        Job("R2", Fraction(1, 2), 1, Fraction(5, 2), "beta"),
        Job("R3", 1, Fraction(5, 2), 6, "alpha"),
        Job("R4", 0, Fraction(1, 2), 3, "beta"),
    ]
    rational_setup = matrix_setup(
        {
            "alpha": {"beta": Fraction(3, 4)},
            "beta": {"alpha": Fraction(1, 2)},
        }
    )
    show("rational instance, unlimited budget", solve(rational_jobs, rational_setup))

    # 5) Idling can beat immediate execution: Jb is free at t=0 but waiting
    # for Ja (released at t=1, tight deadline) is strictly better.
    idle_jobs = [
        Job("Ja", 1, 1, 2, "X"),
        Job("Jb", 0, 10, 1000, "X"),
    ]
    show("idle-is-better instance", solve(idle_jobs, constant_setup(0)))


if __name__ == "__main__":
    main()
