"""Runnable synthetic demo: python3.11 sample.py

Scenario (all data is synthetic):
  sensor shard  : raw readings -> avg-temp   (no assumptions, root)
  model shard   : avg-temp     -> risk-score (assumes sensor export)
  report shard  : risk-score   -> report     (assumes model export)
Then: a duplicate proof submission, and a cyclic pair that must be rejected.
"""
from proofstitch import (
    Assumption,
    Fact,
    Rule,
    RuleRegistry,
    ShardProof,
    Step,
    Stitcher,
)


def build_registry() -> RuleRegistry:
    registry = RuleRegistry()
    registry.register(
        Rule("avg", 1, lambda ps: {"value": round(sum(p["value"] for p in ps) / len(ps), 2)})
    )
    registry.register(Rule("risk", 1, lambda ps: {"score": round(ps[0]["value"] * 1.5, 2)}))
    registry.register(Rule("render", 1, lambda ps: {"text": f"final-risk={ps[0]['score']}"}))
    registry.register(Rule("wrap", 1, lambda ps: {"wrapped": ps[0]}))
    return registry


def sensor_proof() -> ShardProof:
    readings = tuple(
        Fact(f"reading-{i}", 1, {"value": v})
        for i, v in enumerate([20.0, 22.0, 24.0], start=1)
    )
    avg = Fact("avg-temp", 1, {"value": 22.0})
    step = Step("step-avg", 1, "avg", 1, tuple(f.fact_id for f in readings), avg)
    return ShardProof("sensor", 1, readings, (), (step,), ("avg-temp",))


def model_proof(avg_fact: Fact) -> ShardProof:
    assume = Assumption("assume-avg", 1, "avg-temp", avg_fact.digest)
    risk = Fact("risk-score", 1, {"score": 33.0})
    step = Step("step-risk", 1, "risk", 1, ("avg-temp",), risk)
    return ShardProof("model", 1, (), (avg_fact,), (step,), ("risk-score",), (assume,))


def report_proof(risk_fact: Fact) -> ShardProof:
    assume = Assumption("assume-risk", 1, "risk-score", risk_fact.digest)
    report = Fact("report", 1, {"text": "final-risk=33.0"})
    step = Step("step-render", 1, "render", 1, ("risk-score",), report)
    return ShardProof("report", 1, (), (risk_fact,), (step,), ("report",), (assume,))


def cyclic_pair():
    """A assumes B's export and B assumes A's export: no root support."""
    ra = Fact("root-a", 1, {"value": 1})
    rb = Fact("root-b", 1, {"value": 2})
    fa = Fact("alpha", 1, {"wrapped": ra.payload})
    fb = Fact("beta", 1, {"wrapped": rb.payload})
    a = ShardProof(
        "shard-a", 1, (ra,), (fb,),
        (Step("step-a", 1, "wrap", 1, ("root-a",), fa),),
        ("alpha",), (Assumption("a-needs-b", 1, "beta", fb.digest),),
    )
    b = ShardProof(
        "shard-b", 1, (rb,), (fa,),
        (Step("step-b", 1, "wrap", 1, ("root-b",), fb),),
        ("beta",), (Assumption("b-needs-a", 1, "alpha", fa.digest),),
    )
    return a, b


def main() -> None:
    registry = build_registry()

    print("=== 1. Linear chain: sensor -> model -> report ===")
    sensor = sensor_proof()
    model = model_proof(Fact("avg-temp", 1, {"value": 22.0}))
    report = report_proof(Fact("risk-score", 1, {"score": 33.0}))

    stitcher = Stitcher(registry)
    stitcher.add_proof(sensor)
    stitcher.add_proof(model)
    stitcher.add_proof(report)
    print(f"duplicate add accepted: {stitcher.add_proof(sensor)}")

    result = stitcher.stitch()
    print(f"grounded shards   : {sorted(result.grounded_shards)}")
    print(f"ungrounded shards : {sorted(result.ungrounded_shards)}")
    print(f"contributions     : {result.contributions}")
    print(f"duplicates ignored: {result.duplicates_ignored}")
    for fact_id, fact in sorted(result.accepted_conclusions.items()):
        print(f"  accepted {fact_id} v{fact.version}: {fact.payload} digest={fact.digest[:12]}...")

    print()
    print("=== 2. Cyclic mutual assumptions (A <-> B), no root support ===")
    a, b = cyclic_pair()
    cyc = Stitcher(registry)
    cyc.add_proof(a)
    cyc.add_proof(b)
    result2 = cyc.stitch()
    print(f"grounded shards   : {sorted(result2.grounded_shards)}")
    print(f"ungrounded shards : {sorted(result2.ungrounded_shards)}")
    print(f"cycles detected   : {result2.cycles}")
    print(f"accepted          : {sorted(result2.accepted_conclusions)}")


if __name__ == "__main__":
    main()
