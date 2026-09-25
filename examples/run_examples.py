"""Run every example, verify the returned witnesses, and print a summary."""
import json
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from cardian import analyze, verify_witness  # noqa: E402
from reference.enumerator import brute_force  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))


def main():
    failures = 0
    for fname in sorted(os.listdir(HERE)):
        if not fname.endswith(".json"):
            continue
        with open(os.path.join(HERE, fname), encoding="utf-8") as fh:
            payload = json.load(fh)
        print(f"=== {fname}: {payload.get('description', '')}")
        result = analyze(
            payload["stats"], payload["query"],
            config=payload.get("config"), budget=payload.get("budget"),
        )
        s = result["search"]
        print(f"    nodes={s['nodes']} leaves={s['leaves']} "
              f"pruned_infeasible={s['pruned_infeasible']} "
              f"pruned_bound={s['pruned_bound']} budget={s['budget']}")
        if not result["consistent"]:
            print(f"    INCONSISTENT, conflict subset: {result['conflict']}")
            ref = brute_force(payload["stats"], payload["query"])
            if ref["feasible"] != 0:
                print("    FAIL: reference enumerator found feasible instances")
                failures += 1
            continue
        if result.get("consistent") is None:
            print(f"    consistency undecided: {result['reason']}")
            continue
        lo, hi = result["min"]["lower"], result["max"]["upper"]
        print(f"    exact={result['exact']} "
              f"min in [{result['min']['lower']}, {result['min']['upper']}] "
              f"max in [{result['max']['lower']}, {result['max']['upper']}] "
              f"estimate={result['estimate']['value']:.3f} "
              f"(is_bound={result['estimate']['is_bound']})")
        for side in ("min", "max"):
            verdict = verify_witness(payload["stats"], payload["query"],
                                     result[side]["witness"])
            status = "ok" if verdict["ok"] else f"FAIL {verdict['errors']}"
            print(f"    {side}-witness claim={result[side]['witness']['claim']} "
                  f"verified={status}")
            if not verdict["ok"]:
                failures += 1
        if result["exact"]:
            ref = brute_force(payload["stats"], payload["query"])
            match = (ref["min"], ref["max"]) == (lo, hi)
            print(f"    reference enumerator: min={ref['min']} max={ref['max']} "
                  f"match={match}")
            if not match:
                failures += 1
        else:
            print("    (budget-limited: bounds are proven but not tight; "
                  "resumable state available)")
    print("FAILURES:", failures)
    return 1 if failures else 0


if __name__ == "__main__":
    raise SystemExit(main())
