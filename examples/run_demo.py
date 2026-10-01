"""Demo: safe model, replayable counterexample, budget-limited PENDING + resume."""
import json
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from modelcheck import (Budget, CounterModel, MutexModel, PermutationNormalizer,
                        replay_trace, run_bfs)


def main():
    print("=== 1. Fixed mutex (n=3): invariant holds ===")
    res = run_bfs(MutexModel(3, fixed=True))
    print(f"status={res.status} states={res.stats.states_discovered} "
          f"layers={res.stats.layers_completed} "
          f"elapsed={res.stats.elapsed_seconds:.3f}s")

    print("\n=== 2. Buggy mutex (n=2): shortest counterexample ===")
    model = MutexModel(2, fixed=False)
    res = run_bfs(model)
    print(f"status={res.status} violated={res.trace.violated_invariant} "
          f"trace_len={len(res.trace)}")
    for i, state in enumerate(res.trace.states):
        if i:
            print(f"    --{res.trace.actions[i - 1]}-->")
        print(f"    {state}")
    print(f"replay_ok={replay_trace(model, None, res.trace)}")

    print("\n=== 3. Symmetric counters + tiny budget: PENDING, evidence, resume ===")
    model = CounterModel(k=4, max_count=3)
    norm = PermutationNormalizer()
    with tempfile.TemporaryDirectory() as tmp:
        res = run_bfs(model, normalizer=norm, budget=Budget(max_layers=3),
                      run_dir=tmp)
        print(f"status={res.status} reason={res.stats.stop_reason}")
        evidence = json.loads(Path(res.frontier_evidence).read_text())
        print(f"unexplored frontier: layer={evidence['unexplored_layer']} "
              f"states={len(evidence['unexplored_frontier'])}")
        print(f"first unexplored state: {evidence['unexplored_frontier'][0]['state']}")
        resumed = run_bfs(model, normalizer=norm, run_dir=tmp, resume=True)
        print(f"resumed: status={resumed.status} "
              f"states={resumed.stats.states_discovered} "
              f"layers={resumed.stats.layers_completed}")


if __name__ == "__main__":
    main()
