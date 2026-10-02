"""Runnable demo: python3.11 run_sample.py"""
import tempfile

from checker import BfsChecker, Status, replay_trace
from models import BinaryTreeModel, CounterModel, GridModel, TokenRingModel
from reference import reference_reach


def show(title):
    print("\n=== %s ===" % title)


def main():
    show("1. Shortest counterexample (grid walk to a bad cell)")
    model = GridModel(5, 5, bad=(3, 2))
    result = BfsChecker(model).run()
    print("status:", result.status.value)
    print("trace length (transitions):", result.trace_length)
    print("trace:", result.trace)
    print("replayable:", replay_trace(model, result.trace))

    show("2. Budget exhausted -> UNDECIDED with frontier evidence")
    model = BinaryTreeModel(depth=12)
    result = BfsChecker(model, budget_states=50).run()
    cov = result.coverage
    print("status:", result.status.value, "| reason:", cov.reason)
    print("explored: %d states, %d layers | unexplored frontier: %d"
          % (cov.states_explored, cov.layers_completed, cov.frontier_size))
    for item in cov.frontier_evidence:
        print("  evidence depth=%(depth)d hash=%(hash)d state=%(state)s" % item)

    show("3. Compression with recovery map (token ring symmetry)")
    model = TokenRingModel(k=4, tokens=(0, 2), bad_together=False)
    checker = BfsChecker(model)
    result = checker.run()
    reachable, dist, _ = reference_reach(model)
    print("status:", result.status.value)
    print("concrete reachable (reference):", len(reachable))
    print("canonical stored (compressed):", len(checker.store))
    print("merged duplicates:", result.coverage.duplicates)
    sample = next(iter(checker.store.recovery.items()))
    print("recovery map sample: canonical %s -> concrete %s" % sample)

    show("4. Checkpoint / resume across layers")
    model = BinaryTreeModel(depth=6)
    with tempfile.TemporaryDirectory() as ck:
        r1 = BfsChecker(model, checkpoint_dir=ck, budget_layers=3).run()
        print("after 3 layers:", r1.status.value,
              "| explored:", r1.coverage.states_explored)
        r2 = BfsChecker(model, checkpoint_dir=ck).run(resume=True)
        # coverage is cumulative across resume
        total = r2.coverage.states_explored
        reachable, _, _ = reference_reach(model)
        print("resumed:", r2.status.value,
              "| total explored:", total, "| reference:", len(reachable),
              "| exact (no dup, no miss):", total == len(reachable))

    show("5. Initial state violates -> length-0 trace")
    model = CounterModel(5, bad_at=0)
    result = BfsChecker(model).run()
    print("status:", result.status.value, "| trace:", result.trace,
          "| trace length:", result.trace_length)

    show("6. Unreachable bad state is not a counterexample")
    model = CounterModel(3, bad_at=10)
    result = BfsChecker(model).run()
    print("status:", result.status.value, "| explored:",
          result.coverage.states_explored)


if __name__ == "__main__":
    main()
