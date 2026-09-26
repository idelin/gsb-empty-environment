"""Runnable samples for the bounded visible-trace comparator."""
from tracecheck import (
    Determinizer,
    SubsetCache,
    Verdict,
    check_witness,
    compare_bounded,
    make_machine,
)


def fmt_subset(subset):
    return "{" + ", ".join(sorted(map(str, subset))) + "}" if subset else "{}"


def show(result, left_name, right_name):
    print(f"  verdict         : {result.verdict.value} (bound={result.bound})")
    print(f"  pairs explored  : {result.explored_pairs}, truncated={result.truncated}")
    if result.verdict is Verdict.DIFFERENT:
        ev = result.evidence
        print(f"  shortest witness: {' '.join(ev.word)}  [{ev.side}]")
        print(f"  replay on {left_name}:")
        for i, subset in enumerate(ev.left_trajectory):
            prefix = " ".join(ev.word[:i]) or "<empty>"
            print(f"    after {prefix:<12} -> {fmt_subset(subset)}")
        print(f"  replay on {right_name}:")
        for i, subset in enumerate(ev.right_trajectory):
            prefix = " ".join(ev.word[:i]) or "<empty>"
            print(f"    after {prefix:<12} -> {fmt_subset(subset)}")


print("== 1. Pure renaming: no behavioural difference ==")
v1 = make_machine(
    {"idle", "busy"},
    {"start", "stop"},
    {("idle", "start", "busy"), ("busy", "stop", "idle")},
    "idle",
    version="v1",
)
v1_renamed = make_machine(
    {"S0", "S1"},
    {"start", "stop"},
    {("S0", "start", "S1"), ("S1", "stop", "S0")},
    "S0",
    version="v1-renamed",
)
show(compare_bounded(v1, v1_renamed, bound=6), "v1", "v1_renamed")

print("\n== 2. Version update: added and removed traces ==")
old = make_machine(
    {"s0", "s1"},
    {"login", "reset", "logout"},
    {("s0", "login", "s1"), ("s1", "reset", "s0")},
    "s0",
    version="v1",
)
new = make_machine(
    {"s0", "s1"},
    {"login", "reset", "logout"},
    {("s0", "login", "s1"), ("s1", "logout", "s0")},
    "s0",
    version="v2",
)
result = compare_bounded(old, new, bound=4)
show(result, "old", "new")
print(
    "  witness replays ok:",
    check_witness(old, new, result.evidence.word, result.evidence.side),
)

print("\n== 3. Tau loops and the empty trace ==")
loop_a = make_machine(
    {"s0", "s1"}, {"a"}, {("s0", None, "s0"), ("s0", "a", "s1")}, "s0", version="v1"
)
loop_only = make_machine({"t0"}, {"a"}, {("t0", None, "t0")}, "t0", version="v1")
show(compare_bounded(loop_a, loop_only, bound=3), "loop_a", "loop_only")
show(compare_bounded(loop_only, loop_only, bound=3), "loop_only", "loop_only")

print("\n== 4. Difference beyond the bound stays unclaimed ==")
aaa = make_machine(
    {"q0", "q1", "q2", "q3"},
    {"a"},
    {("q0", "a", "q1"), ("q1", "a", "q2"), ("q2", "a", "q3")},
    "q0",
)
aa = make_machine(
    {"q0", "q1", "q2"}, {"a"}, {("q0", "a", "q1"), ("q1", "a", "q2")}, "q0"
)
show(compare_bounded(aaa, aa, bound=2), "aaa", "aa")
show(compare_bounded(aaa, aa, bound=3), "aaa", "aa")

print("\n== 5. Budget exhausted: verdict stays UNKNOWN ==")
show(compare_bounded(aaa, aa, bound=3, max_pairs=0), "aaa", "aa")

print("\n== 6. Cache reuse and invalidation on version update ==")
cache = SubsetCache()
compare_bounded(old, new, bound=4, cache=cache)
print(f"  cache entries after first run : {len(cache)}")
print(f"  cold lookup for old@v1        : {Determinizer(old, cache).cache_hit}")
new_v3 = make_machine(
    {"s0", "s1"},
    {"login", "reset", "logout"},
    {("s0", "login", "s1"), ("s1", "logout", "s0"), ("s1", "reset", "s1")},
    "s0",
    version="v3",
)
print(f"  stale hit for updated new@v3  : {Determinizer(new_v3, cache).cache_hit}")
result = compare_bounded(old, new_v3, bound=4, cache=cache)
show(result, "old", "new_v3")
removed = cache.invalidate_version("v2")
print(f"  entries dropped for version v2: {removed}; cache now holds {len(cache)}")
