"""Demo models and runnable scenarios for the liveness checker.

Run with: python3.11 examples.py
"""
from __future__ import annotations

from checker import check, replay
from fsm import Model


def reliable_service() -> Model:
    """Every request is processed and answered; the pending region is
    acyclic, so no non-fulfilling cycle exists."""
    model = Model("reliable-service", "idle")
    model.add_state("idle")
    model.add_state("busy", labels={"req"})
    model.add_state("served", labels={"resp"})
    model.add_transition("idle", "request", "busy")
    model.add_transition("busy", "process", "served")
    model.add_transition("served", "reset", "idle")
    return model


def lossy_service() -> Model:
    """After a request the server may spin in `wait` forever; the
    self-loop `spin` is a non-fulfilling cycle on its own. `respond`
    stays enabled the whole time, so declaring weak fairness on
    {"respond"} excludes that loop."""
    model = Model("lossy-service", "idle")
    model.add_state("idle")
    model.add_state("wait", labels={"req"})
    model.add_state("done", labels={"resp"})
    model.add_transition("idle", "request", "wait")
    model.add_transition("wait", "spin", "wait")
    model.add_transition("wait", "respond", "done")
    model.add_transition("done", "reset", "idle")
    return model


def deep_service() -> Model:
    """A long boot chain before the request logic; used to show that a
    truncated search reports PENDING instead of jumping to a verdict."""
    model = Model("deep-service", "boot0")
    for stage in range(6):
        model.add_state(f"boot{stage}")
        model.add_transition(f"boot{stage}", "init", f"boot{stage + 1}")
    model.add_state("boot6")
    model.add_state("wait", labels={"req"})
    model.add_state("done", labels={"resp"})
    model.add_transition("boot6", "request", "wait")
    model.add_transition("wait", "spin", "wait")
    model.add_transition("wait", "respond", "done")
    model.add_transition("done", "reset", "boot0")
    return model


def show(title, result, model=None, fairness_sets=()):
    evidence = result.evidence
    print(f"== {title}")
    print(f"verdict: {result.verdict.value}")
    print(f"explored: {evidence.explored_nodes} nodes, "
          f"{evidence.explored_edges} edges, truncated={evidence.truncated}")
    declared = [sorted(group) for group in evidence.fairness_sets]
    print(f"fairness sets declared: {declared if declared else 'none'}")
    for ignored in evidence.ignored_unfair_components:
        missing = [sorted(group) for group in ignored.missing_fairness]
        print(f"ignored unfair cycle over states {ignored.states}; "
              f"it never takes and always enables {missing}")
    if evidence.counterexample is not None:
        print("counterexample:")
        print(evidence.counterexample.format())
        report = replay(model, evidence.counterexample, fairness_sets)
        print(f"replay: {'OK' if report.ok else 'FAILED'} "
              f"({len(report.checks)} checks)")
        print(report.format())
    print()


def main():
    show("reliable service, no fairness assumptions",
         check(reliable_service()),
         model=reliable_service())

    show("lossy service, no fairness assumptions",
         check(lossy_service()),
         model=lossy_service())

    show("lossy service, weak fairness on {respond}",
         check(lossy_service(), fairness_sets=[{"respond"}]),
         model=lossy_service(), fairness_sets=[{"respond"}])

    show("lossy service, weak fairness on {spin} only "
         "(respond gets no assumption)",
         check(lossy_service(), fairness_sets=[{"spin"}]),
         model=lossy_service(), fairness_sets=[{"spin"}])

    show("deep service, search truncated at 4 nodes",
         check(deep_service(), max_nodes=4))

    show("deep service, full search",
         check(deep_service()),
         model=deep_service())


if __name__ == "__main__":
    main()
