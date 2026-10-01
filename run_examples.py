"""Demo: run the POR checker and full enumeration on the example models.

Usage:  python3.11 run_examples.py
"""

from __future__ import annotations

from porcheck.checker import (
    PENDING,
    check,
    compare_with_full,
    validate_counterexample,
)
from examples import models


def show_comparison(model) -> None:
    cmp = compare_with_full(model)
    por, full = cmp.por, cmp.full
    print(f"== {model.name} ==")
    print(
        f"  POR : verdict={por.verdict:6s} states={por.states_explored:4d} "
        f"transitions={por.transitions:4d} evidence={por.evidence_summary()}"
    )
    print(
        f"  FULL: verdict={full.verdict:6s} states={full.states_explored:4d} "
        f"transitions={full.transitions:4d}"
    )
    if cmp.conclusive:
        print(f"  verdicts agree: {cmp.verdicts_agree}")
    else:
        print("  verdicts agree: (inconclusive, search pending)")
    if por.trace is not None:
        print(f"  counterexample ({por.violation}):")
        print(por.trace)
        validate_counterexample(model, por.trace, por.violation)
        print("  replay: OK, counterexample re-executes to the violation")
    print()


def show_evidence(model, limit: int = 4) -> None:
    result = check(model, reduction=True)
    print(f"== reduction evidence for {model.name} (first {limit} decisions) ==")
    for decision in result.decisions[:limit]:
        print(f"  {decision}")
    print()


def main() -> None:
    show_comparison(models.independent_counters(3, 4))
    show_comparison(models.independent_counters(2, 2, deadlock_is_error=True))
    show_comparison(models.shared_counter(2))
    show_comparison(models.copy_violation(2))
    show_comparison(models.lock_deadlock())

    print("== cycle proviso guard (model: cycle_ignoring) ==")
    model = models.cycle_ignoring()
    naive = check(model, reduction=True, cycle_proviso=False)
    guarded = check(model, reduction=True, cycle_proviso=True)
    full = check(model, reduction=False)
    print(f"  without proviso: verdict={naive.verdict} (violation missed!)")
    print(f"  with proviso   : verdict={guarded.verdict} ({guarded.violation})")
    print(f"  full enum      : verdict={full.verdict}")
    print("  counterexample:")
    print(guarded.trace)
    validate_counterexample(model, guarded.trace, guarded.violation)
    print("  replay: OK")
    print()

    print("== pending when the budget runs out (model: independent_counters(50,50)) ==")
    model = models.independent_counters(50, 50)
    limited = check(model, reduction=True, max_states=100)
    assert limited.verdict == PENDING
    print(
        f"  max_states=100 -> verdict={limited.verdict} "
        f"(explored {limited.states_explored} states, no conclusive claim)"
    )
    print()

    show_evidence(models.independent_counters(3, 4))
    show_evidence(models.cycle_ignoring())


if __name__ == "__main__":
    main()
