"""Unit tests for the fact-resolution engine (synthetic data only)."""

from __future__ import annotations

import itertools
import json
import unittest
from datetime import datetime, timezone

from fact_engine import (
    DEFAULT_RULES,
    ENGINE_ID,
    ENGINE_VERSION,
    Fact,
    SuppressionReason,
    TruthValue,
    build_evidence,
    replay,
    resolve,
    verify,
)
from fact_engine.replay import dumps, loads

UTC = timezone.utc
CLAIM = "server-room.temperature.safe"
T0 = datetime(2026, 1, 1, tzinfo=UTC)
T1 = datetime(2026, 1, 10, tzinfo=UTC)
INSIDE = datetime(2026, 1, 5, tzinfo=UTC)
AFTER = datetime(2026, 1, 20, tzinfo=UTC)


def fact(fid, version, value, level, start=T0, end=None, source="s"):
    return Fact(fid, version, CLAIM, value, level, start, end, source)


def reasons(decision, ref):
    return {s.reason for s in decision.suppressed if s.fact_ref == ref}


class LevelPreferenceTests(unittest.TestCase):
    def test_higher_level_wins(self):
        facts = [fact("low", 1, True, 1), fact("high", 1, False, 3)]
        d = resolve(CLAIM, facts, INSIDE)
        self.assertIs(d.value, TruthValue.FALSE)
        self.assertEqual(d.winners, ["high@v1"])
        self.assertEqual(reasons(d, "low@v1"), {SuppressionReason.OUTRANKED})

    def test_same_level_agreement_decides(self):
        facts = [fact("a", 1, True, 2), fact("b", 1, True, 2)]
        d = resolve(CLAIM, facts, INSIDE)
        self.assertIs(d.value, TruthValue.TRUE)
        self.assertEqual(d.winners, ["a@v1", "b@v1"])


class SameLevelConflictTests(unittest.TestCase):
    def test_conflict_stays_undecided(self):
        facts = [fact("a", 1, True, 3), fact("b", 1, False, 3)]
        d = resolve(CLAIM, facts, INSIDE)
        self.assertIs(d.value, TruthValue.UNDECIDED)
        self.assertEqual(d.winners, [])
        self.assertEqual(d.contesting, ["a@v1", "b@v1"])
        self.assertEqual(d.suppressed, [])

    def test_conflict_is_not_arbitrary(self):
        forward = resolve(CLAIM, [fact("a", 1, True, 3), fact("b", 1, False, 3)], INSIDE)
        backward = resolve(CLAIM, [fact("b", 1, False, 3), fact("a", 1, True, 3)], INSIDE)
        self.assertIs(forward.value, TruthValue.UNDECIDED)
        self.assertEqual(forward.digest(), backward.digest())

    def test_conflict_suppresses_lower_level(self):
        facts = [
            fact("a", 1, True, 3),
            fact("b", 1, False, 3),
            fact("low", 1, True, 1),
        ]
        d = resolve(CLAIM, facts, INSIDE)
        self.assertIs(d.value, TruthValue.UNDECIDED)
        self.assertEqual(reasons(d, "low@v1"), {SuppressionReason.OUTRANKED})


class ValidityWindowTests(unittest.TestCase):
    def test_expiry_reactivates_lower_level(self):
        facts = [
            fact("low", 1, True, 1),
            fact("high", 1, False, 3, end=T1),
        ]
        during = resolve(CLAIM, facts, INSIDE)
        self.assertIs(during.value, TruthValue.FALSE)
        self.assertEqual(reasons(during, "low@v1"), {SuppressionReason.OUTRANKED})
        after = resolve(CLAIM, facts, AFTER)
        self.assertIs(after.value, TruthValue.TRUE)
        self.assertEqual(after.winners, ["low@v1"])
        self.assertEqual(reasons(after, "high@v1"), {SuppressionReason.EXPIRED})

    def test_not_yet_effective(self):
        future = datetime(2026, 2, 1, tzinfo=UTC)
        facts = [fact("future", 1, True, 5, start=future)]
        d = resolve(CLAIM, facts, INSIDE)
        self.assertIs(d.value, TruthValue.UNDECIDED)
        self.assertEqual(reasons(d, "future@v1"), {SuppressionReason.NOT_YET_EFFECTIVE})

    def test_boundary_end_is_exclusive(self):
        facts = [fact("x", 1, True, 2, end=T1)]
        d = resolve(CLAIM, facts, T1)
        self.assertIs(d.value, TruthValue.UNDECIDED)
        self.assertEqual(reasons(d, "x@v1"), {SuppressionReason.EXPIRED})

    def test_open_ended_interval_stays_effective(self):
        facts = [fact("x", 1, False, 2)]
        d = resolve(CLAIM, facts, datetime(2030, 1, 1, tzinfo=UTC))
        self.assertIs(d.value, TruthValue.FALSE)


class VersionSupersedeTests(unittest.TestCase):
    def test_older_version_superseded(self):
        facts = [fact("x", 1, True, 2), fact("x", 2, False, 2)]
        d = resolve(CLAIM, facts, INSIDE)
        self.assertIs(d.value, TruthValue.FALSE)
        self.assertEqual(d.winners, ["x@v2"])
        self.assertEqual(reasons(d, "x@v1"), {SuppressionReason.SUPERSEDED})

    def test_supersede_is_order_independent(self):
        a = resolve(CLAIM, [fact("x", 1, True, 2), fact("x", 2, False, 2)], INSIDE)
        b = resolve(CLAIM, [fact("x", 2, False, 2), fact("x", 1, True, 2)], INSIDE)
        self.assertEqual(a.digest(), b.digest())


class OrderIndependenceTests(unittest.TestCase):
    def test_all_permutations_same_digest(self):
        facts = [
            fact("low", 0, True, 1),
            fact("low", 1, True, 1),
            fact("mid", 1, False, 2, end=T1),
            fact("high-a", 1, False, 3, end=T1),
            fact("high-b", 1, True, 3, end=T1),
        ]
        digests = {
            resolve(CLAIM, list(perm), INSIDE).digest()
            for perm in itertools.permutations(facts)
        }
        self.assertEqual(len(digests), 1)


class StepAndIdTests(unittest.TestCase):
    def test_steps_have_stable_ids_and_rule_versions(self):
        facts = [fact("low", 1, True, 1), fact("high", 1, False, 3)]
        d = resolve(CLAIM, facts, INSIDE)
        self.assertEqual(
            [s.step_id for s in d.steps],
            [
                "step-01-version-supersede",
                "step-02-validity-window",
                "step-03-prefer-high-level",
                "step-04-same-level-conflict",
            ],
        )
        rule_versions = {r.rule_id: r.version for r in DEFAULT_RULES}
        for step in d.steps:
            self.assertEqual(step.version, 1)
            self.assertEqual(step.rule_version, rule_versions[step.rule_id])

    def test_engine_identity_recorded(self):
        d = resolve(CLAIM, [fact("x", 1, True, 1)], INSIDE)
        self.assertEqual(d.engine_id, ENGINE_ID)
        self.assertEqual(d.engine_version, ENGINE_VERSION)


class ReplayTests(unittest.TestCase):
    def setUp(self):
        self.facts = [
            fact("low", 0, True, 1),
            fact("low", 1, True, 1),
            fact("high-a", 1, False, 3, end=T1),
            fact("high-b", 1, True, 3, end=T1),
        ]

    def test_replay_roundtrip_verifies(self):
        evidence = build_evidence(CLAIM, self.facts, INSIDE)
        restored = loads(dumps(evidence))
        self.assertTrue(verify(restored))
        self.assertEqual(replay(restored).digest(), evidence["digest"])

    def test_tampered_fact_fails_verification(self):
        evidence = json.loads(dumps(build_evidence(CLAIM, self.facts, INSIDE)))
        evidence["facts"][2]["value"] = not evidence["facts"][2]["value"]
        self.assertFalse(verify(evidence))

    def test_tampered_decision_fails_verification(self):
        evidence = json.loads(dumps(build_evidence(CLAIM, self.facts, INSIDE)))
        evidence["decision"]["value"] = "true"
        self.assertFalse(verify(evidence))

    def test_wrong_engine_version_fails(self):
        evidence = json.loads(dumps(build_evidence(CLAIM, self.facts, INSIDE)))
        evidence["engine"]["version"] = "0.0.0"
        self.assertFalse(verify(evidence))

    def test_unsupported_format_rejected(self):
        evidence = build_evidence(CLAIM, self.facts, INSIDE)
        evidence["format"] = "other/9"
        with self.assertRaises(ValueError):
            replay(evidence)


if __name__ == "__main__":
    unittest.main()
