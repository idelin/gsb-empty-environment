"""End-to-end demo: merge, mark, contradict, correct, diff, verify."""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from logmerge import (
    CausalCycleError,
    Event,
    InfeasibleError,
    LogMerger,
    OffsetInterval,
    OffsetTable,
    WallClock,
    verify_markers,
)

TABLE = OffsetTable({
    "web": OffsetInterval(-5, 5),      # ms, true = wall + offset
    "db": OffsetInterval(-5, 5),
    "cache": OffsetInterval(90, 110),  # cache clock runs ~100 ms behind
})

EVENTS = [
    Event("w1", "web", WallClock(1000, "web"), "recv request"),
    Event("w2", "web", WallClock(1050, "web"), "send response", causes=("d2",)),
    Event("d1", "db", WallClock(1005, "db"), "query", causes=("w1",)),
    Event("d2", "db", WallClock(1020, "db"), "write"),
    # wall clock 960 looks *earliest*, but its offset and the causal edge
    # d2 -> c1 place it after d2.
    Event("c1", "cache", WallClock(960, "cache"), "invalidate", causes=("d2",)),
]


def show(version):
    r = version.result
    print(f"== version {version.number}: {version.note}")
    for i, eid in enumerate(r.order):
        lo, hi = r.time_bounds[eid]
        print(f"  {i}: {eid:4s} true-time in [{lo.at}, {hi.at}]")
    print("  adjacent pairs:")
    for m in r.adjacent:
        tag = "NECESSARY" if m.necessary else "uncertain"
        print(f"    {m.before} -> {m.after}: {tag}")
    if r.uncertain_pairs:
        print("  uncertain pairs: " + ", ".join(f"{a}~{b}" for a, b in sorted(r.uncertain_pairs)))
    problems = verify_markers(r)
    print("  marker verification:", "OK" if not problems else problems)
    print()


def main():
    merger = LogMerger(TABLE)
    v1 = merger.build(EVENTS, note="initial merge")
    show(v1)

    # --- contradiction: cache event claimed to cause the web request ---
    print("== contradictory input (causal edge c1 -> w1 is impossible under the offsets)")
    bad = [
        Event("w1", "web", WallClock(1000, "web"), "recv request", causes=("c1",)),
        Event("c1", "cache", WallClock(960, "cache"), "invalidate"),
    ]
    try:
        merger.build(bad, note="should fail")
    except InfeasibleError as exc:
        print(f"  rejected, constraint ring returned, times unmodified:")
        for c in exc.ring:
            print(f"    {c}")
    print()

    # --- correction: c1's wall clock was mislogged ---
    v2 = merger.correct("c1", wall=WallClock(1030, "cache"), note="fix c1 wall clock")
    show(v2)
    print(merger.diff(v1, v2))
    print()

    # --- old readers keep their version ---
    old = merger.get_version(1)
    print(f"== old reader still sees v{old.number}: {' '.join(old.result.order)}")
    print(f"   v{old.number} uncertain pairs: {sorted(old.result.uncertain_pairs)}")


if __name__ == "__main__":
    main()
