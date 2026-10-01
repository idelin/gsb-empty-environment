"""End-to-end demo of the bounded Petri net verification toolkit.

Run from the repository root:  python3 examples/demo.py
"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from petri import (
    CertificateCache,
    PetriNet,
    SymmetryReduction,
    Transition,
    check_invariant,
    cross_check,
    reachability,
    replay_dict,
)


def section(title):
    print("\n" + "=" * 66)
    print(title)
    print("=" * 66)


# --------------------------------------------------------------------- #
section("1. Bounded producer/consumer: execution, paths, invariant check")
net = PetriNet(
    capacities={"p_ready": 1, "c_ready": 1, "buffer": 2},
    transitions=[
        Transition.make("produce", inputs={"p_ready": 1},
                        outputs={"p_ready": 1, "buffer": 1}),
        Transition.make("consume", inputs={"buffer": 1, "c_ready": 1},
                        outputs={"c_ready": 1}),
    ],
    initial={"p_ready": 1, "c_ready": 1},
)
res = reachability(net)
print(f"semantics        : {res.semantics} (no coverability approximation)")
print(f"status           : {res.status}")
print(f"reachable states : {len(res.states)}")
for key in sorted(res.states):
    print(f"  {net.marking_dict(res.concrete(key))}")

inv = check_invariant(net, lambda m: m["buffer"] <= 2,
                      description="buffer within capacity")
print(f"\ninvariant 'buffer <= 2'      : holds={inv.holds} ({inv.status})")
inv = check_invariant(net, lambda m: m["buffer"] <= 1,
                      description="buffer never exceeds 1")
cex = inv.counterexample
print(f"invariant 'buffer <= 1'      : holds={inv.holds} ({inv.status})")
print(f"  counterexample trace   : {cex.trace}")
print(f"  counterexample marking : {cex.marking}  (real place IDs)")
print(f"  replayed marking       : {cex.replay(net)}  (replay OK)")

section("2. Truncated search stays PENDING (no verdict)")
inv = check_invariant(net, lambda m: m["buffer"] <= 2, max_states=2)
print(f"max_states=2 -> status={inv.status}, holds={inv.holds}")

# --------------------------------------------------------------------- #
section("3. Read arcs and inhibitor arcs")
net2 = PetriNet(
    capacities={"lock": 1, "count": 2, "alarm": 1},
    transitions=[
        Transition.make("tick", reads={"lock": 1}, outputs={"count": 1}),
        Transition.make("reset", inhibitors={"count": 1},
                        inputs={"alarm": 1}, outputs={"lock": 1}),
        Transition.make("ring", inhibitors={"count": 1}, outputs={"alarm": 1}),
    ],
    initial={"lock": 1},
)
m = net2.initial
t = net2.transitions["tick"]
print(f"initial            : {net2.marking_dict(m)}")
print(f"tick enabled (read lock present): {net2.is_enabled(t, m)}")
m = net2.fire(t, m)
print(f"after tick         : {net2.marking_dict(m)}  (lock not consumed)")
m = net2.fire(net2.transitions["tick"], m)
print(f"after tick         : {net2.marking_dict(m)}  (count at capacity -> tick disabled: "
      f"{net2.is_enabled(t, m)})")
print(f"ring enabled while count>0 (inhibitor): "
      f"{net2.is_enabled(net2.transitions['ring'], m)}")

# --------------------------------------------------------------------- #
section("4. Invariant certificates + incremental revalidation")
ring = PetriNet(
    capacities={"a": 2, "b": 2, "c": 2},
    transitions=[
        Transition.make("t1", inputs={"a": 1}, outputs={"b": 1}),
        Transition.make("t2", inputs={"b": 1}, outputs={"c": 1}),
        Transition.make("t3", inputs={"c": 1}, outputs={"a": 1}),
    ],
    initial={"a": 1},
)
cache = CertificateCache(ring)
cert = cache.register("total", {"a": 1, "b": 1, "c": 1})
print(f"certificate 'total': weights={cert.weights}, constant={cert.constant}, "
      f"status={cert.status}")
res_ring = reachability(ring)
print(f"holds on all {len(res_ring.states)} reachable markings: "
      f"{cache.check_reachable('total', res_ring)}")

ring.modify_transition("t1", reads={"c": 1})
print(f"\nafter adding read arc to t1 (rev {ring.revision}):")
print(f"  revalidate -> {cache.revalidate()}  "
      f"(rechecked only: {sorted(cache.get('total').checked_transitions)})")

ring.modify_transition("t2", outputs={"c": 2})
print(f"after t2 produces 2 tokens (rev {ring.revision}):")
print(f"  revalidate -> {cache.revalidate()}  "
      f"(violations: {sorted(cache.get('total').violations)})")
ring.modify_transition("t2", outputs={"c": 1})
print(f"after repairing t2 (rev {ring.revision}):")
print(f"  revalidate -> {cache.revalidate()}")

# --------------------------------------------------------------------- #
section("5. Terminal-state token totals vs. net invariant")
net3 = PetriNet(
    capacities={"s": 2, "d": 2},
    transitions=[Transition.make("move", inputs={"s": 1}, outputs={"d": 1})],
    initial={"s": 1},
)
cache3 = CertificateCache(net3)
cache3.register("total", {"s": 1, "d": 1})  # all-ones: total token count
res3 = reachability(net3)
report = cache3.verify_terminal_token_totals("total", res3)
for key, ok in sorted(report.items()):
    print(f"  deadlock {net3.marking_dict(res3.concrete(key))}: "
          f"token total matches invariant -> {ok}")

# --------------------------------------------------------------------- #
section("6. Symmetry reduction + independent plain-search cross-check")
workers = PetriNet(
    capacities={**{f"idle_{i}": 1 for i in range(3)},
                **{f"busy_{i}": 1 for i in range(3)}, "sched": 1},
    transitions=[
        t for i in range(3) for t in (
            Transition.make(f"wake_{i}", inputs={f"idle_{i}": 1},
                            outputs={f"busy_{i}": 1}, reads={"sched": 1}),
            Transition.make(f"sleep_{i}", inputs={f"busy_{i}": 1},
                            outputs={f"idle_{i}": 1}),
        )
    ],
    initial={**{f"idle_{i}": 1 for i in range(3)}, "sched": 1},
)
red = SymmetryReduction(workers)
print(f"automorphisms found: {len(red.permutations)}")
plain = reachability(workers)
reduced = reachability(workers, reduction=red)
print(f"plain reachable states   : {len(plain.states)}")
print(f"reduced reachable states : {len(reduced.states)} (canonical classes)")
report = cross_check(workers)
print(f"cross-check (reduced vs. independent plain search): ok={report.ok} "
      f"- {report.details}")
sample = reduced.concrete(sorted(reduced.states)[-1])
print(f"enabling preserved under canonicalisation on all states: "
      f"{all(red.preserves_enabling(reduced.concrete(k)) for k in reduced.states)}")

inv = check_invariant(
    workers,
    lambda m: any(m[f"idle_{i}"] == 1 for i in range(3)),
    reduction=red,
    description="at least one worker idle",
)
cex = inv.counterexample
print(f"\ninvariant 'some worker idle': holds={inv.holds}")
print(f"  counterexample trace   : {cex.trace}")
print(f"  counterexample marking : {cex.marking}  (real place IDs)")
print(f"  replayed               : {cex.replay(workers) == cex.marking}")
