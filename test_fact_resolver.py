import copy
import unittest

from fact_resolver import RULES, STEPS, replay, resolve_facts


def evidence(evidence_id, reliability, source=None, version="1.0.0"):
    return {
        "evidence_id": evidence_id,
        "version": version,
        "source": source or f"synthetic:{evidence_id}",
        "reliability": reliability,
        "synthetic": True,
    }


def fact(
    fact_id,
    claim_id,
    value,
    reliability,
    valid_from="2026-01-01T00:00:00Z",
    valid_until="2027-01-01T00:00:00Z",
    evidence_id=None,
    version="1.0.0",
):
    return {
        "fact_id": fact_id,
        "version": version,
        "claim_id": claim_id,
        "value": value,
        "evidence": evidence(evidence_id or f"E-{fact_id}", reliability),
        "valid_from": valid_from,
        "valid_until": valid_until,
    }


class FactResolverTest(unittest.TestCase):
    def test_highest_reliability_wins_and_lower_is_suppressed(self):
        result = resolve_facts(
            [
                fact("F-LOW", "claim.a", True, 1),
                fact("F-MEDIUM", "claim.a", True, 2),
                fact("F-HIGH", "claim.a", False, 3),
            ],
            "2026-06-01T00:00:00Z",
        ).canonical()

        claim = result["claims"][0]
        self.assertEqual(claim["status"], "decided")
        self.assertIs(claim["value"], False)
        self.assertEqual(claim["winning_tier"], 3)
        self.assertEqual([item["fact_id"] for item in result["selected"]], ["F-HIGH"])
        self.assertEqual(
            {(item["fact_id"], item["reason_code"]) for item in result["suppressed"]},
            {
                ("F-LOW", "LOWER_TIER_NOT_PREFERRED"),
                ("F-MEDIUM", "LOWER_TIER_NOT_PREFERRED"),
            },
        )

    def test_same_tier_true_false_conflict_is_undecided(self):
        result = resolve_facts(
            [
                fact("F-TRUE", "claim.a", True, 2),
                fact("F-FALSE", "claim.a", False, 2),
                fact("F-LOW", "claim.a", True, 1),
            ],
            "2026-06-01T00:00:00Z",
        ).canonical()

        claim = result["claims"][0]
        self.assertEqual(claim["status"], "undecided")
        self.assertIsNone(claim["value"])
        self.assertEqual(
            sorted(item["fact_id"] for item in claim["conflicting_evidence"]),
            ["F-FALSE", "F-TRUE"],
        )
        self.assertEqual(result["selected"], [])
        self.assertEqual(
            {(item["fact_id"], item["reason_code"]) for item in result["suppressed"]},
            {
                ("F-FALSE", "PEER_TIER_CONFLICT_UNRESOLVED"),
                ("F-TRUE", "PEER_TIER_CONFLICT_UNRESOLVED"),
                ("F-LOW", "BLOCKED_BY_UNRESOLVED_PEER_CONFLICT"),
            },
        )

    def test_expired_high_tier_allows_lower_tier_to_become_effective(self):
        facts = [
            fact(
                "F-LOW",
                "claim.a",
                True,
                1,
                valid_from="2026-01-01T00:00:00Z",
                valid_until="2027-01-01T00:00:00Z",
            ),
            fact(
                "F-HIGH",
                "claim.a",
                False,
                3,
                valid_from="2026-01-01T00:00:00Z",
                valid_until="2026-07-01T00:00:00Z",
            ),
        ]

        active = resolve_facts(facts, "2026-06-30T23:59:59Z").canonical()
        expired = resolve_facts(facts, "2026-07-01T00:00:00Z").canonical()

        self.assertIs(active["claims"][0]["value"], False)
        self.assertEqual(expired["claims"][0]["status"], "decided")
        self.assertIs(expired["claims"][0]["value"], True)
        self.assertEqual(expired["claims"][0]["winning_tier"], 1)
        self.assertEqual(expired["inactive"][0]["fact_id"], "F-HIGH")
        self.assertEqual(expired["inactive"][0]["reason_code"], "EXPIRED")

    def test_future_fact_is_reported_inactive(self):
        result = resolve_facts(
            [
                fact(
                    "F-FUTURE",
                    "claim.a",
                    True,
                    3,
                    valid_from="2027-01-01T00:00:00Z",
                    valid_until="2028-01-01T00:00:00Z",
                )
            ],
            "2026-06-01T00:00:00Z",
        ).canonical()

        self.assertEqual(result["claims"], [])
        self.assertEqual(result["inactive"][0]["reason_code"], "NOT_YET_EFFECTIVE")

    def test_empty_input_is_replayable(self):
        result = resolve_facts([], "2026-06-01T00:00:00Z").canonical()

        self.assertEqual(result["claims"], [])
        self.assertFalse(result["synthetic_data"])
        self.assertTrue(replay(result["replay"]["canonical_operation"])["matches"])

    def test_input_order_does_not_affect_result_or_replay_digest(self):
        facts = [
            fact("F-A", "claim.a", True, 1),
            fact("F-B", "claim.a", False, 3),
            fact("F-C", "claim.a", True, 2),
            fact("F-D", "claim.b", False, 2),
            fact("F-E", "claim.b", True, 2),
        ]
        first = resolve_facts(facts, "2026-06-01T00:00:00Z").canonical()
        reordered = resolve_facts(list(reversed(facts)), "2026-06-01T00:00:00Z").canonical()

        self.assertEqual(first, reordered)
        self.assertEqual(
            first["replay"]["digest"],
            reordered["replay"]["digest"],
        )

    def test_replay_recomputes_digest_and_detects_tampered_fact(self):
        rendered = resolve_facts(
            [fact("F-A", "claim.a", False, 2)],
            "2026-06-01T00:00:00Z",
        ).canonical()
        operation = rendered["replay"]["canonical_operation"]

        self.assertTrue(replay(operation)["matches"])

        tampered = copy.deepcopy(operation)
        tampered["facts"][0]["value"] = True
        self.assertNotEqual(_digest(tampered), rendered["replay"]["digest"])

    def test_replay_rejects_changed_rule_version(self):
        rendered = resolve_facts(
            [fact("F-A", "claim.a", True, 1)],
            "2026-06-01T00:00:00Z",
        ).canonical()
        operation = rendered["replay"]["canonical_operation"]
        operation["rules"][0]["version"] = "9.9.9"

        with self.assertRaisesRegex(ValueError, "rule IDs or versions"):
            replay(operation)

    def test_all_rules_and_steps_have_stable_ids_and_versions(self):
        self.assertTrue(all(rule.rule_id and rule.version for rule in RULES.values()))
        self.assertTrue(all(step.step_id and step.version for step in STEPS.values()))
        for step in STEPS.values():
            self.assertIn(step.rule_id, RULES)

    def test_duplicate_fact_version_is_rejected(self):
        facts = [
            fact("F-A", "claim.a", True, 1, version="1.0.0"),
            fact("F-A", "claim.a", False, 1, version="1.0.0"),
        ]
        with self.assertRaisesRegex(ValueError, "duplicate fact"):
            resolve_facts(facts, "2026-06-01T00:00:00Z")


def _digest(operation):
    import hashlib
    import json

    encoded = json.dumps(
        operation,
        sort_keys=True,
        separators=(",", ":"),
        ensure_ascii=False,
        allow_nan=False,
    ).encode("utf-8")
    return hashlib.sha256(encoded).hexdigest()


if __name__ == "__main__":
    unittest.main()
