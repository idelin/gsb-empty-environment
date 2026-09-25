import unittest

from proofstitch import (
    Assumption,
    Fact,
    ReplayError,
    Rule,
    RuleRegistry,
    ShardProof,
    Step,
    StitchError,
    Stitcher,
)


def make_registry() -> RuleRegistry:
    registry = RuleRegistry()
    registry.register(
        Rule("avg", 1, lambda ps: {"value": round(sum(p["value"] for p in ps) / len(ps), 2)})
    )
    registry.register(Rule("risk", 1, lambda ps: {"score": round(ps[0]["value"] * 1.5, 2)}))
    registry.register(Rule("render", 1, lambda ps: {"text": f"final-risk={ps[0]['score']}"}))
    registry.register(Rule("wrap", 1, lambda ps: {"wrapped": ps[0]}))
    return registry


AVG_FACT = Fact("avg-temp", 1, {"value": 22.0})
RISK_FACT = Fact("risk-score", 1, {"score": 33.0})


def sensor_proof() -> ShardProof:
    readings = tuple(
        Fact(f"reading-{i}", 1, {"value": v})
        for i, v in enumerate([20.0, 22.0, 24.0], start=1)
    )
    step = Step("step-avg", 1, "avg", 1, tuple(f.fact_id for f in readings), AVG_FACT)
    return ShardProof("sensor", 1, readings, (), (step,), ("avg-temp",))


def model_proof(avg_fact: Fact = AVG_FACT, version: int = 1) -> ShardProof:
    assume = Assumption("assume-avg", 1, "avg-temp", avg_fact.digest)
    step = Step("step-risk", 1, "risk", 1, ("avg-temp",), RISK_FACT)
    return ShardProof(
        "model", version, (), (avg_fact,), (step,), ("risk-score",), (assume,)
    )


def report_proof(risk_fact: Fact = RISK_FACT) -> ShardProof:
    assume = Assumption("assume-risk", 1, "risk-score", risk_fact.digest)
    report = Fact("report", 1, {"text": "final-risk=33.0"})
    step = Step("step-render", 1, "render", 1, ("risk-score",), report)
    return ShardProof("report", 1, (), (risk_fact,), (step,), ("report",), (assume,))


def chain_stitcher() -> Stitcher:
    stitcher = Stitcher(make_registry())
    stitcher.add_proof(sensor_proof())
    stitcher.add_proof(model_proof())
    stitcher.add_proof(report_proof())
    return stitcher


class TestLinearChain(unittest.TestCase):
    def test_chain_grounds_all_shards(self):
        result = chain_stitcher().stitch()
        self.assertEqual(result.grounded_shards, {"sensor", "model", "report"})
        self.assertEqual(result.ungrounded_shards, set())
        self.assertEqual(result.cycles, [])
        self.assertEqual(
            set(result.accepted_conclusions), {"avg-temp", "risk-score", "report"}
        )
        self.assertEqual(
            result.contributions, {"sensor": 1, "model": 1, "report": 1}
        )

    def test_evidence_is_replayable_and_deterministic(self):
        first = chain_stitcher().stitch()
        second = chain_stitcher().stitch()
        self.assertEqual(
            {k: f.digest for k, f in first.accepted_conclusions.items()},
            {k: f.digest for k, f in second.accepted_conclusions.items()},
        )


class TestCycles(unittest.TestCase):
    def _cyclic_stitcher(self) -> Stitcher:
        # Each shard derives its export from a LOCAL root fact, but declares
        # an assumption on the other's export: A needs B, B needs A, no root.
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
        stitcher = Stitcher(make_registry())
        stitcher.add_proof(a)
        stitcher.add_proof(b)
        return stitcher

    def test_mutual_assumptions_without_root_are_rejected(self):
        result = self._cyclic_stitcher().stitch()
        self.assertEqual(result.grounded_shards, set())
        self.assertEqual(result.ungrounded_shards, {"shard-a", "shard-b"})
        self.assertEqual(result.cycles, [["shard-a", "shard-b"]])
        self.assertEqual(result.accepted_conclusions, {})
        self.assertEqual(result.contributions, {})

    def test_cycle_does_not_poison_grounded_chain(self):
        stitcher = chain_stitcher()
        ra = Fact("root-a", 1, {"value": 1})
        rb = Fact("root-b", 1, {"value": 2})
        fa = Fact("alpha", 1, {"wrapped": ra.payload})
        fb = Fact("beta", 1, {"wrapped": rb.payload})
        stitcher.add_proof(ShardProof(
            "shard-a", 1, (ra,), (fb,),
            (Step("step-a", 1, "wrap", 1, ("root-a",), fa),),
            ("alpha",), (Assumption("a-needs-b", 1, "beta", fb.digest),),
        ))
        stitcher.add_proof(ShardProof(
            "shard-b", 1, (rb,), (fa,),
            (Step("step-b", 1, "wrap", 1, ("root-b",), fb),),
            ("beta",), (Assumption("b-needs-a", 1, "alpha", fa.digest),),
        ))
        result = stitcher.stitch()
        self.assertEqual(result.grounded_shards, {"sensor", "model", "report"})
        self.assertEqual(result.ungrounded_shards, {"shard-a", "shard-b"})
        self.assertEqual(result.cycles, [["shard-a", "shard-b"]])

    def test_self_assumption_cannot_ground_itself(self):
        root = Fact("root-self", 1, {"value": 1})
        fact = Fact("alpha", 1, {"wrapped": root.payload})
        proof = ShardProof(
            "selfish", 1, (root,), (fact,),
            (Step("step-self", 1, "wrap", 1, ("root-self",), fact),),
            ("alpha",), (Assumption("self-need", 1, "alpha", fact.digest),),
        )
        stitcher = Stitcher(make_registry())
        stitcher.add_proof(proof)
        result = stitcher.stitch()
        self.assertEqual(result.grounded_shards, set())
        self.assertEqual(result.ungrounded_shards, {"selfish"})


class TestDigestMatching(unittest.TestCase):
    def test_digest_mismatch_is_rejected(self):
        # Model shard is internally consistent but pins a STALE avg-temp
        # digest that does not match the sensor shard's actual export.
        stale_avg = Fact("avg-temp", 1, {"value": 21.0})
        stale_risk = Fact("risk-score", 1, {"score": 31.5})
        assume = Assumption("assume-avg", 1, "avg-temp", stale_avg.digest)
        step = Step("step-risk", 1, "risk", 1, ("avg-temp",), stale_risk)
        model = ShardProof(
            "model", 1, (), (stale_avg,), (step,), ("risk-score",), (assume,)
        )
        stitcher = Stitcher(make_registry())
        stitcher.add_proof(sensor_proof())
        stitcher.add_proof(model)
        with self.assertRaises(StitchError):
            stitcher.stitch()

    def test_missing_evidence_leaves_shard_ungrounded(self):
        stitcher = Stitcher(make_registry())
        stitcher.add_proof(model_proof())  # no sensor shard present
        result = stitcher.stitch()
        self.assertEqual(result.grounded_shards, set())
        self.assertEqual(result.ungrounded_shards, {"model"})
        self.assertEqual(result.cycles, [])


class TestReplay(unittest.TestCase):
    def test_tampered_step_fails_replay(self):
        readings = (Fact("reading-1", 1, {"value": 20.0}),)
        fake = Fact("avg-temp", 1, {"value": 999.0})  # not avg([20.0])
        step = Step("step-avg", 1, "avg", 1, ("reading-1",), fake)
        proof = ShardProof("sensor", 1, readings, (), (step,), ("avg-temp",))
        stitcher = Stitcher(make_registry())
        stitcher.add_proof(proof)
        with self.assertRaises(ReplayError):
            stitcher.stitch()

    def test_unknown_rule_version_fails(self):
        readings = (Fact("reading-1", 1, {"value": 20.0}),)
        step = Step("step-avg", 1, "avg", 99, ("reading-1",), AVG_FACT)
        proof = ShardProof("sensor", 1, readings, (), (step,), ("avg-temp",))
        stitcher = Stitcher(make_registry())
        stitcher.add_proof(proof)
        with self.assertRaises(KeyError):
            stitcher.stitch()


class TestDuplicates(unittest.TestCase):
    def test_duplicate_proof_not_counted_twice(self):
        stitcher = Stitcher(make_registry())
        self.assertTrue(stitcher.add_proof(sensor_proof()))
        self.assertFalse(stitcher.add_proof(sensor_proof()))  # exact duplicate
        result = stitcher.stitch()
        self.assertEqual(result.contributions, {"sensor": 1})
        self.assertEqual(result.duplicates_ignored, [("sensor", 1)])

    def test_new_version_is_a_distinct_proof(self):
        stitcher = Stitcher(make_registry())
        stitcher.add_proof(sensor_proof())
        readings = (Fact("reading-9", 1, {"value": 30.0}),)
        avg30 = Fact("avg-temp", 2, {"value": 30.0})
        step = Step("step-avg", 2, "avg", 1, ("reading-9",), avg30)
        self.assertTrue(
            stitcher.add_proof(
                ShardProof("sensor", 2, readings, (), (step,), ("avg-temp",))
            )
        )
        result = stitcher.stitch()
        self.assertEqual(result.contributions, {"sensor": 2})


if __name__ == "__main__":
    unittest.main()
