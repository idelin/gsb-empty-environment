"""Runnable demo of the TMS engine (synthetic data, stdlib only).

Scenario required by the spec:
    A and B jointly imply C (rule r1); D alone also implies C (rule r2).
    Retracting B keeps C (still supported by D via r2);
    retracting D afterwards retracts C.
Also demonstrated: duplicate facts do not inflate support counts, and a
rule cycle without external fact support cannot sustain its conclusions.
"""

from tms import Engine, Fact, Rule


def show(engine, label):
    print("== %s" % label)
    for node in sorted(engine.in_nodes):
        print("   IN  %-4s supports=%s" % (node, sorted(engine.support_of(node))))
    for node in ("A", "B", "C", "D", "X", "Y"):
        if not engine.is_in(node):
            print("   OUT %s" % node)
    print()


def main():
    engine = Engine()

    engine.add_rule(Rule("r1", 1, {"A", "B"}, "C"))  # conjunctive premises
    engine.add_rule(Rule("r2", 1, {"D"}, "C"))       # alternative rule
    engine.add_rule(Rule("r3", 1, {"X"}, "Y"))       # cycle X <-> Y
    engine.add_rule(Rule("r4", 1, {"Y"}, "X"))

    engine.assert_fact(Fact("fA", 1, "A"))
    engine.assert_fact(Fact("fB", 1, "B"))
    engine.assert_fact(Fact("fD", 1, "D"))
    show(engine, "A, B, D asserted -> C IN via r1 and r2")

    dup = engine.assert_fact(Fact("fA", 1, "A"))  # duplicate fact
    print("duplicate assert of fA: noop=%s, support_count(C)=%d (unchanged)"
          % (dup.noop, engine.support_count("C")))
    print()

    engine.retract_fact("fB")
    show(engine, "retract B -> C still IN (minimal support {r2})")

    engine.retract_fact("fD")
    show(engine, "retract D -> C loses its last support and goes OUT")

    show(engine, "cycle X<->Y has no external support -> both stay OUT")

    engine.assert_fact(Fact("fX", 1, "X"))
    show(engine, "assert X -> cycle becomes grounded, X and Y IN")

    engine.retract_fact("fX")
    show(engine, "retract X -> cycle alone cannot sustain X and Y")

    report = engine.replay()
    print("replay: ok=%s, steps_checked=%d (every recorded state hash reproduced)"
          % (report.ok, report.steps_checked))
    print("final state digest: %s" % engine.state_digest())


if __name__ == "__main__":
    main()
