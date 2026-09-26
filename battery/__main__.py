"""Runnable sample: python3.11 -m battery

Three scenarios: (1) an 8-period arbitrage instance with a negative price
and a budget, (2) an infeasible final-energy target, (3) a negative-price
instance showing that forbidding same-period bidirectional cycling keeps
the profit bounded.
"""
from __future__ import annotations

from fractions import Fraction

from .model import BudgetStatus, Status, build_problem
from .solver import solve


def show(result) -> None:
    print(f"status        : {result.status.value}")
    if result.status is Status.INFEASIBLE:
        print(f"message       : {result.message}")
        if result.budget_status is not None:
            print(f"budget status : {result.budget_status.value} "
                  f"(budget={result.budget})")
        return
    header = ("t", "price", "demand", "charge", "discharge",
              "delivered", "grid", "energy", "cost")
    print(" ".join(f"{h:>10}" for h in header))
    for d in result.schedule:
        row = (d.period, d.price, d.demand, d.charge, d.discharge,
               d.delivered, d.grid_import, d.energy_after, d.cost)
        print(" ".join(f"{str(v):>10}" for v in row))
    print(f"optimal cost  : {result.optimal_cost} "
          f"(= {float(result.optimal_cost):.4f})")
    if result.budget_status is not None:
        print(f"budget status : {result.budget_status.value} "
              f"(budget={result.budget})")


def main() -> None:
    print("=== Scenario 1: 8-period arbitrage, negative price, budget ===")
    problem = build_problem(
        prices=[4, 2, 1, -2, 1, 3, 6, 5],
        demands=[3, 2, 2, 1, 4, 5, 6, 4],
        capacity=12,
        max_charge_power=6,
        max_discharge_power=5,
        charge_efficiency=Fraction(9, 10),
        discharge_efficiency=Fraction(9, 10),
        initial_energy=3,
        final_energy=3,
        budget=75,
    )
    show(solve(problem))

    print()
    print("=== Scenario 2: infeasible final energy (budget undecided) ===")
    problem = build_problem(
        prices=[1, 1],
        demands=[0, 0],
        capacity=10,
        max_charge_power=3,
        max_discharge_power=3,
        charge_efficiency=Fraction(1, 2),
        discharge_efficiency=Fraction(1, 2),
        initial_energy=0,
        final_energy=5,  # at most 3 stored in 2 periods -> infeasible
        budget=0,
    )
    show(solve(problem))

    print()
    print("=== Scenario 3: negative price, cycling forbidden ===")
    problem = build_problem(
        prices=[-5],
        demands=[0],
        capacity=100,
        max_charge_power=5,
        max_discharge_power=5,
        charge_efficiency=Fraction(1, 2),
        discharge_efficiency=Fraction(1, 2),
        initial_energy=0,
        final_energy=0,
    )
    show(solve(problem))
    print("note: with same-period cycling allowed, charging 5 and releasing "
          "the\nstored 5/2 in the same period would earn 5*(5 - 5/4) = 75/4 "
          "from thin\nair; the no-cycling rule keeps the optimum at 0.")


if __name__ == "__main__":
    main()
