"""Runnable sample: compare two versions of a small order-service model.

Run with:  python3.11 -m tracecheck.demo
"""

from .compare import BehaviorComparator, Verdict, compare
from .fsm import TAU, StateMachine


def order_service_v1():
    return StateMachine(
        states={"idle", "placed", "paid", "shipped"},
        initial="idle",
        transitions=[
            ("idle", "order", "placed"),
            ("placed", TAU, "placed"),          # internal retry loop, invisible
            ("placed", "pay", "paid"),
            ("paid", TAU, "shipped"),           # internal handoff, invisible
            ("shipped", "notify", "shipped"),
        ],
        version="order-v1",
    )


def order_service_v2():
    # v2: states renamed, plus a new visible "cancel" trace from "placed".
    return StateMachine(
        states={"s0", "s1", "s2", "s3"},
        initial="s0",
        transitions=[
            ("s0", "order", "s1"),
            ("s1", TAU, "s1"),
            ("s1", "pay", "s2"),
            ("s2", TAU, "s3"),
            ("s3", "notify", "s3"),
            ("s1", "cancel", "s0"),             # new behavior in v2
        ],
        version="order-v2",
    )


def order_service_v2_renamed_only():
    # v2': pure renaming of v1, no behavioral change.
    return StateMachine(
        states={"s0", "s1", "s2", "s3"},
        initial="s0",
        transitions=[
            ("s0", "order", "s1"),
            ("s1", TAU, "s1"),
            ("s1", "pay", "s2"),
            ("s2", TAU, "s3"),
            ("s3", "notify", "s3"),
        ],
        version="order-v2-renamed",
    )


def show(title, result):
    print(f"== {title} ==")
    print(f"verdict: {result.verdict.value} (bound={result.bound}, "
          f"explored={result.explored} subset pairs)")
    if result.counterexample is not None:
        print(result.counterexample.explain())
    print()


def main():
    v1 = order_service_v1()
    v2 = order_service_v2()
    renamed = order_service_v2_renamed_only()

    show("v1 vs renamed v1 (pure renaming)",
         compare(v1, renamed, bound=6))

    result = compare(v1, v2, bound=6)
    show("v1 vs v2 (v2 adds 'cancel')", result)
    print(f"counterexample replays on both machines: "
          f"{result.counterexample.replay(v1, v2)}\n")

    show("v1 vs v2 with tiny step budget (stays pending)",
         compare(v1, v2, bound=6, max_steps=1))

    comparator = BehaviorComparator()
    comparator.compare(v1, v2, bound=6)
    comparator.compare(v1, v2, bound=6)
    print(f"== cache ==\nafter two identical compares: "
          f"hits={comparator.hits} misses={comparator.misses}")
    v3 = v2.updated(version="order-v3",
                    transitions=v2.transitions + (("s1", "refund", "s0"),))
    comparator.compare(v1, v3, bound=6)
    print(f"after updating v2 -> v3:          "
          f"hits={comparator.hits} misses={comparator.misses} "
          f"(stale entry not reused)")


if __name__ == "__main__":
    main()
