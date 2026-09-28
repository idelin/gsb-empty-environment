"""Sample histories and their verdicts.  Run with:  python -m linqueue"""
from __future__ import annotations

from .model import QueueModel, Ret, FULL
from .checker import Verdict, check
from .history import enq, deq, pending_enq
from .counterexample import minimize, replay_counterexample, replay_witness


def _show(title, model, ops, budget=100_000):
    print(f"== {title} ==")
    for op in ops:
        print(f"  {op}")
    result = check(model, ops, budget)
    print(f"  verdict: {result.verdict.value} "
          f"(explored {result.explored} states, budget {result.budget})")

    if result.verdict is Verdict.LINEARIZABLE:
        final_state = replay_witness(model, result.linearization)
        print("  witness order (replayed OK):")
        print("    " + " -> ".join(str(op) for op in result.linearization))
        print(f"  final model state: {tuple(final_state)}")

    elif result.verdict is Verdict.NOT_LINEARIZABLE:
        core = minimize(model, ops, budget)
        print(f"  minimal counterexample ({len(core)} of {len(ops)} calls):")
        for op in core:
            print(f"    {op}")
        report = replay_counterexample(model, core)
        print(f"  replay: {report.orders_explored} candidate sequential "
              f"explanations, every one rejected by the model")
        print("  first rejection:")
        print(report.failures[0].describe())

    print()


def main() -> None:
    print("linqueue demo -- linearizability of small concurrent queue histories\n")

    _show(
        "1. later-invoked call returns first; completion-time sort is wrong",
        QueueModel(capacity=2),
        [
            enq(1, "A", invoke=0, response=10),   # slow enq A
            enq(2, "B", invoke=1, response=2),    # later, returns first
            deq(3, invoke=3, response=4, result=Ret("ok", "A")),
        ],
    )

    _show(
        "2. legal EMPTY exception on an empty queue",
        QueueModel(capacity=1),
        [
            enq(1, "A", invoke=0, response=1),
            deq(2, invoke=2, response=3, result=Ret("ok", "A")),
            deq(3, invoke=4, response=5),  # -> EMPTY
        ],
    )

    _show(
        "3. deq returns a value that was never enqueued -> counterexample",
        QueueModel(capacity=1),
        [
            enq(1, "A", invoke=0, response=1),
            deq(2, invoke=2, response=3, result=Ret("ok", "B")),
        ],
    )

    _show(
        "4. pending enq must take effect (only source of the dequeued value)",
        QueueModel(capacity=1),
        [
            pending_enq(1, "A", invoke=0),          # never returns
            deq(2, invoke=1, response=2, result=Ret("ok", "A")),
        ],
    )

    _show(
        "5. pending enq can be dropped (EMPTY still explainable)",
        QueueModel(capacity=1),
        [
            pending_enq(1, "A", invoke=0),
            deq(2, invoke=1, response=2),  # -> EMPTY
        ],
    )

    _show(
        "6. EMPTY while an enq completed earlier -> counterexample",
        QueueModel(capacity=2),
        [
            enq(1, "A", invoke=0, response=1),
            deq(2, invoke=2, response=3),  # -> EMPTY, impossible
            enq(3, "B", invoke=10, response=11),  # irrelevant later call
        ],
    )

    _show(
        "7. legal FULL exception on a capacity-1 queue",
        QueueModel(capacity=1),
        [
            enq(1, "A", invoke=0, response=1),
            enq(2, "B", invoke=2, response=3, result=FULL),
            deq(3, invoke=4, response=5, result=Ret("ok", "A")),
        ],
    )

    _show(
        "8. budget exhausted before proof completes -> undecided",
        QueueModel(capacity=2),
        [
            enq(1, "A", invoke=0, response=1),
            deq(2, invoke=2, response=3, result=Ret("ok", "A")),
        ],
        budget=0,
    )


if __name__ == "__main__":
    main()
