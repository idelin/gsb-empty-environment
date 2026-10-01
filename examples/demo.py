"""End-to-end demo: finite-capacity Petri net execution, reachability,
invariant certificates, symmetry reduction, incremental revalidation.

Run:  python3.11 examples/demo.py
"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from petrinet import (PetriNet, Place, Transition, InvariantCertificate,
                      CertificateStore, SearchLimits, search, cross_check,
                      replay, verify_certificate)


def build_line_net():
    """Bounded production line with read and inhibitor arcs.

    raw --t_load--> buf --t_unload--> done
    t_inspect : read arc, needs done >= 2 (observes, consumes nothing)
    t_recycle : inhibitor arc on buf (only fires when buf is empty),
                moves 3 tokens done -> raw
    """
    places = [Place("raw", 3), Place("buf", 2), Place("done", 3)]
    transitions = [
        Transition("t_load", inputs={"raw": 1}, outputs={"buf": 1}),
        Transition("t_unload", inputs={"buf": 1}, outputs={"done": 1}),
        Transition("t_inspect", read={"done": 2}),
        Transition("t_recycle", inputs={"done": 3}, outputs={"raw": 3},
                   inhibitor={"buf": 1}),
    ]
    return PetriNet(places, transitions)


def build_symmetric_net():
    """Two identical machines; swapping A<->B is a symmetry."""
    places = [Place("idleA", 1), Place("busyA", 1),
              Place("idleB", 1), Place("busyB", 1)]
    transitions = [
        Transition("startA", inputs={"idleA": 1}, outputs={"busyA": 1}),
        Transition("stopA", inputs={"busyA": 1}, outputs={"idleA": 1}),
        Transition("startB", inputs={"idleB": 1}, outputs={"busyB": 1}),
        Transition("stopB", inputs={"busyB": 1}, outputs={"idleB": 1}),
    ]
    swap = {"idleA": "idleB", "idleB": "idleA",
            "busyA": "busyB", "busyB": "busyA"}
    return PetriNet(places, transitions), swap


def main():
    print("=== 1. Bounded line: capacity / read / inhibitor arcs ===")
    net = build_line_net()
    initial = {"raw": 3}
    conservation = InvariantCertificate(
        "conservation", {"raw": 1, "buf": 1, "done": 1})
    print("net fingerprint:", net.fingerprint()[:16], "...")
    print("certificate 'conservation' verified:",
          verify_certificate(net, conservation))

    res = search(net, initial, certificates=[conservation])
    print(f"verdict={res.verdict}  states={len(res.markings)}  "
          f"terminals={len(res.terminal_markings)}")
    print("invariant holds on all markings:", res.certificates_ok)
    print("invariant holds on terminal markings:",
          res.terminal_certificates_ok,
          f"({len(res.terminal_markings)} terminals; t_inspect/t_recycle "
          "keep the line live)")

    print("\n--- batch net: terminal token total checked by invariant ---")
    batch = PetriNet([Place("raw", 2), Place("done", 2)],
                     [Transition("t", inputs={"raw": 1},
                                 outputs={"done": 1})])
    bcert = InvariantCertificate("total", {"raw": 1, "done": 1})
    bres = search(batch, {"raw": 2}, certificates=[bcert])
    for m in bres.terminal_markings:
        d = batch.decode(m)
        print("  terminal:", d, " token total:", sum(d.values()),
              " invariant ok:", bres.terminal_certificates_ok)

    print("\n--- property: done >= 1 ---")
    bad = search(net, initial, bad=lambda m: m["done"] >= 1,
                 certificates=[conservation])
    ce = bad.counterexample
    print("verdict:", bad.verdict)
    print("counterexample path:", " -> ".join(ce.transitions))
    print("final marking (real place ids):", ce.final)
    print("replay ok:", ce.verify(net), " json roundtrip ok:",
          type(ce).from_json(ce.to_json()).verify(net))

    print("\n--- property: buf == 2 and done == 2 (impossible, total is 3) ---")
    safe = search(net, initial,
                  bad=lambda m: m["buf"] == 2 and m["done"] == 2,
                  certificates=[conservation])
    print("verdict:", safe.verdict)

    print("\n--- truncated search stays pending ---")
    pend = search(net, initial, limits=SearchLimits(max_states=3))
    print("verdict:", pend.verdict, " explored:", pend.explored)

    print("\n=== 2. Symmetry reduction vs. independent plain search ===")
    sym_net, swap = build_symmetric_net()
    cc = cross_check(sym_net, {"idleA": 1, "idleB": 1}, [swap])
    print(f"full states={cc['full_states']}  reduced states="
          f"{cc['reduced_states']}  orbits={cc['orbits']}  "
          f"orbit sets match: {cc['match']}")

    print("\n=== 3. Incremental certificate revalidation ===")
    store = CertificateStore(net)
    print("register:", store.register(conservation))
    net.replace_transition(Transition("t_load", inputs={"raw": 1},
                                      outputs={"buf": 2}))
    print("after widening t_load output -> revalidate:", store.revalidate())
    net.replace_transition(Transition("t_load", inputs={"raw": 1},
                                      outputs={"buf": 1}))
    print("after restoring t_load        -> revalidate:", store.revalidate())

    print("\n=== 4. Finite capacity is NOT unbounded coverability ===")
    cov = PetriNet([Place("p", 1), Place("q", 1), Place("leak", 1)],
                   [Transition("move", inputs={"q": 1}, outputs={"p": 1,
                                                                 "q": 1}),
                    Transition("escape", inputs={"p": 2},
                               outputs={"leak": 1})])
    r = search(cov, {"q": 1}, bad=lambda m: m["leak"] >= 1)
    print("unbounded net could stack 2 tokens in p and fire 'escape';")
    print("with capacity 1, 'move' is disabled when p is full.")
    print("verdict for leak >= 1:", r.verdict,
          " reachable markings:", len(r.markings))


if __name__ == "__main__":
    main()
