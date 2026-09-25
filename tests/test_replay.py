import json
import unittest

from proofdag.errors import CycleError, ReplayError
from proofdag.model import Fact, Rule, Step
from proofdag.replay import replay
from proofdag.store import ProofStore

CONJ = Rule("conj-intro", 1, commutative=True)
CHAIN = Rule("ordered-chain", 1, commutative=False)

SEMANTICS = {
    ("conj-intro", 1): lambda ps: " ∧ ".join(sorted(ps)),
    ("ordered-chain", 1): lambda ps: ps[0] + " ⇒ " + ps[-1],
}


def build_store():
    store = ProofStore()
    a = Fact("a", 1, "sensor-alpha", "A")
    b = Fact("b", 1, "sensor-beta", "B")
    conj = Step("s-conj", 1, CONJ, (a, b), Fact("c", 1, "derived", "A ∧ B"))
    chain = Step("s-chain", 1, CHAIN, (conj, a), Fact("d", 1, "derived", "A ∧ B ⇒ A"))
    store.mark_root(store.add_step(chain))
    return store


class ReplayTests(unittest.TestCase):
    def test_evidence_roundtrip_replays(self):
        evidence = build_store().evidence()
        form = replay(evidence, SEMANTICS)
        self.assertEqual(form.digest, evidence["digest"])

    def test_evidence_survives_json_roundtrip(self):
        evidence = build_store().evidence()
        clone = json.loads(json.dumps(evidence))
        form = replay(clone, SEMANTICS)
        self.assertEqual(form.digest, evidence["digest"])

    def test_replay_is_deterministic(self):
        evidence = build_store().evidence()
        self.assertEqual(replay(evidence, SEMANTICS), replay(evidence, SEMANTICS))

    def test_tampered_evidence_rejected(self):
        evidence = build_store().evidence()
        evidence["nodes"][0]["fact"]["payload"] = "forged"
        with self.assertRaises(ReplayError):
            replay(evidence, SEMANTICS)

    def test_semantics_violation_rejected(self):
        evidence = build_store().evidence()
        wrong = {
            ("conj-intro", 1): lambda ps: " | ".join(sorted(ps)),
            ("ordered-chain", 1): lambda ps: ps[0] + " ⇒ " + ps[-1],
        }
        with self.assertRaises(ReplayError):
            replay(evidence, wrong)

    def test_cyclic_evidence_rejected(self):
        evidence = {
            "format": "proofdag-evidence/1",
            "digest": "x",
            "roots": [0],
            "nodes": [
                {"number": 0, "kind": "derived",
                 "fact": {"fact_id": "a", "version": 1,
                          "source_id": "d", "payload": "A"},
                 "rule": {"rule_id": "r", "version": 1, "commutative": False},
                 "premises": [1]},
                {"number": 1, "kind": "derived",
                 "fact": {"fact_id": "b", "version": 1,
                          "source_id": "d", "payload": "B"},
                 "rule": {"rule_id": "r", "version": 1, "commutative": False},
                 "premises": [0]},
            ],
        }
        with self.assertRaises(CycleError):
            replay(evidence)

    def test_bad_format_rejected(self):
        with self.assertRaises(ReplayError):
            replay({"format": "other/1"})


if __name__ == "__main__":
    unittest.main()
