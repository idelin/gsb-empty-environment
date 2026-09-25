"""Replayable evidence: determinism, verification, tamper detection."""
import copy
import unittest

from secexplain.engine import Engine
from secexplain.model import PUBLIC
from secexplain.projection import project
from secexplain.replay import build_evidence, replay
from secexplain.sampledata import build_world


class ReplayTest(unittest.TestCase):
    def setUp(self):
        self.world = build_world(chain_len=3, extra_hidden=2)
        self.derivation = Engine(self.world.rules).derive(self.world.facts)
        self.expl = project(self.derivation, "approved", PUBLIC)

    def test_evidence_replays_ok(self):
        evidence = build_evidence(self.expl, self.derivation)
        self.assertTrue(replay(evidence))

    def test_replay_is_deterministic(self):
        first = build_evidence(self.expl, self.derivation)
        second = build_evidence(self.expl, self.derivation)
        self.assertEqual(first, second)
        self.assertEqual(first["digest"], second["digest"])

    def test_evidence_contains_no_hidden_details(self):
        evidence = build_evidence(self.expl, self.derivation)
        blob = str(evidence)
        for token in ("risk_cleared", "F-SCREEN", "F-NOTE", "R-CHAIN",
                      "screen_0"):
            self.assertNotIn(token, blob)

    def test_tampered_fact_value_fails_replay(self):
        evidence = build_evidence(self.expl, self.derivation)
        tampered = copy.deepcopy(evidence)
        tampered["facts"][0]["value"] = "false"
        self.assertFalse(replay(tampered))

    def test_tampered_digest_fails_replay(self):
        evidence = build_evidence(self.expl, self.derivation)
        tampered = dict(evidence, digest="0" * 64)
        self.assertFalse(replay(tampered))

    def test_tampered_step_id_fails_replay(self):
        evidence = build_evidence(self.expl, self.derivation)
        tampered = copy.deepcopy(evidence)
        tampered["steps"][0]["step_id"] = "step-" + "0" * 16
        tampered["tree"]["support"]["step_id"] = "step-" + "0" * 16
        # Fix the digest so only the step-id recomputation can catch it.
        import hashlib, json
        body = {k: v for k, v in tampered.items() if k != "digest"}
        tampered["digest"] = hashlib.sha256(
            json.dumps(body, sort_keys=True).encode()).hexdigest()
        self.assertFalse(replay(tampered))

    def test_wrong_schema_version_fails_replay(self):
        evidence = build_evidence(self.expl, self.derivation)
        self.assertFalse(replay(dict(evidence, schema=999)))


if __name__ == "__main__":
    unittest.main()
