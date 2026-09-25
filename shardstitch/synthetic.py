"""合成样例数据：ingest -> analytics -> reporting 三分片流水线。"""
from __future__ import annotations

from .model import Assumption, Fact, Proof, Rule, Shard, Step


def build_ingest() -> Shard:
    facts = (Fact("reading.temp.1", 1, "21.5C"), Fact("reading.temp.2", 1, "22.0C"))
    rule = Rule("rule.normalize", 1, "join", "batch.normalized", 1)
    step = Step(
        "step.normalize", 1, "rule.normalize", 1,
        (("reading.temp.1", 1), ("reading.temp.2", 1)),
        ("batch.normalized", 1),
    )
    proof = Proof("proof.normalize", 1, ("batch.normalized", 1), facts=facts, steps=(step,))
    return Shard("ingest", 1, rules=(rule,), proofs=(proof,))


def build_analytics(ingest: Shard, expected_digest: str | None = None) -> Shard:
    upstream = ingest.proofs[0]
    assumption = Assumption(
        "assume.batch", "batch.normalized", 1,
        "21.5C|22.0C",
        expected_digest if expected_digest is not None else upstream.digest(),
    )
    fact = Fact("threshold.temp", 1, "25C")
    rule = Rule("rule.summarize", 1, "join", "summary.daily", 1)
    step = Step(
        "step.summarize", 1, "rule.summarize", 1,
        (("batch.normalized", 1), ("threshold.temp", 1)),
        ("summary.daily", 1),
    )
    proof = Proof(
        "proof.summarize", 1, ("summary.daily", 1),
        facts=(fact,), assumptions=(assumption,), steps=(step,),
    )
    return Shard("analytics", 1, rules=(rule,), proofs=(proof,))


def build_reporting(analytics: Shard) -> Shard:
    upstream = analytics.proofs[0]
    assumption = Assumption(
        "assume.summary", "summary.daily", 1,
        "21.5C|22.0C|25C",
        upstream.digest(),
    )
    fact = Fact("report.format", 1, "daily-brief")
    rule = Rule("rule.render", 1, "join", "report.daily", 1)
    step = Step(
        "step.render", 1, "rule.render", 1,
        (("report.format", 1), ("summary.daily", 1)),
        ("report.daily", 1),
    )
    proof = Proof(
        "proof.render", 1, ("report.daily", 1),
        facts=(fact,), assumptions=(assumption,), steps=(step,),
    )
    return Shard("reporting", 1, rules=(rule,), proofs=(proof,))


def build_chain() -> list[Shard]:
    ingest = build_ingest()
    analytics = build_analytics(ingest)
    reporting = build_reporting(analytics)
    return [ingest, analytics, reporting]


def build_mutual_assumption_pair() -> list[Shard]:
    """A 假设 B、B 假设 A 的无根互假设对。

    由于证据摘要是内容寻址的，双方无法构造出互相匹配的摘要
    （那需要哈希不动点），因此该对必然被拒绝、无任何结论接地。
    """
    fact_a = Fact("reading.a", 1, "a0")
    fact_b = Fact("reading.b", 1, "b0")
    rule_a = Rule("rule.a", 1, "join", "conclusion.a", 1)
    rule_b = Rule("rule.b", 1, "join", "conclusion.b", 1)

    placeholder = "0" * 64
    assume_b_v0 = Assumption("assume.b", "conclusion.b", 1, "b0", placeholder)
    proof_a_v0 = Proof(
        "proof.a", 1, ("conclusion.a", 1),
        facts=(fact_a,), assumptions=(assume_b_v0,),
        steps=(Step("step.a", 1, "rule.a", 1,
                    (("reading.a", 1), ("conclusion.b", 1)), ("conclusion.a", 1)),),
    )
    assume_a = Assumption("assume.a", "conclusion.a", 1, "a0|b0", proof_a_v0.digest())
    proof_b = Proof(
        "proof.b", 1, ("conclusion.b", 1),
        facts=(fact_b,), assumptions=(assume_a,),
        steps=(Step("step.b", 1, "rule.b", 1,
                    (("reading.b", 1), ("conclusion.a", 1)), ("conclusion.b", 1)),),
    )
    assume_b = Assumption("assume.b", "conclusion.b", 1, "b0", proof_b.digest())
    proof_a = Proof(
        "proof.a", 1, ("conclusion.a", 1),
        facts=(fact_a,), assumptions=(assume_b,),
        steps=(Step("step.a", 1, "rule.a", 1,
                    (("reading.a", 1), ("conclusion.b", 1)), ("conclusion.a", 1)),),
    )
    return [
        Shard("shard.a", 1, rules=(rule_a,), proofs=(proof_a,)),
        Shard("shard.b", 1, rules=(rule_b,), proofs=(proof_b,)),
    ]
