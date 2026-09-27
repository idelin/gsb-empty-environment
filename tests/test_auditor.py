import json
import unittest

from dp_audit import mechanisms
from dp_audit.auditor import AuditConfig, format_summary, run_audit


def make_config(mechanism, trials=4000, seed=123, epsilon=1.0, threshold=1.0):
    return AuditConfig(
        mechanism=mechanism,
        base_dataset=(0,) * 10,
        injected_record=1,
        event=lambda out: out >= threshold,
        event_description=f"output >= {threshold:g}",
        n_trials=trials,
        alpha=0.05,
        claimed_epsilon=epsilon,
        seed=seed,
    )


class TestReproducibility(unittest.TestCase):
    def test_same_seed_same_counts(self):
        r1 = run_audit(make_config(mechanisms.laplace_count(1.0)))
        r2 = run_audit(make_config(mechanisms.laplace_count(1.0)))
        self.assertEqual(
            r1["results"]["base"]["event_count"], r2["results"]["base"]["event_count"]
        )
        self.assertEqual(
            r1["results"]["injected"]["event_count"],
            r2["results"]["injected"]["event_count"],
        )

    def test_seed_and_trials_recorded(self):
        report = run_audit(make_config(mechanisms.laplace_count(1.0), seed=99, trials=500))
        self.assertEqual(report["parameters"]["seed"], 99)
        self.assertEqual(report["parameters"]["n_trials_per_dataset"], 500)
        self.assertEqual(report["results"]["base"]["trials"], 500)

    def test_report_is_json_serializable(self):
        report = run_audit(make_config(mechanisms.deterministic_count()))
        json.dumps(report)  # must not raise


class TestVerdicts(unittest.TestCase):
    def test_valid_mechanism_not_flagged(self):
        report = run_audit(make_config(mechanisms.laplace_count(1.0)))
        self.assertFalse(report["verdict"]["violation_detected"])
        self.assertIn("NOT a proof", report["verdict"]["explanation"])

    def test_overclaimed_privacy_flagged(self):
        # laplace_count(1.0) only satisfies epsilon = 1.0; claiming 0.01
        # must be falsified by the audit.
        report = run_audit(make_config(mechanisms.laplace_count(1.0), epsilon=0.01))
        self.assertTrue(report["verdict"]["violation_detected"])

    def test_leaky_mechanism_flagged_with_reproducible_event(self):
        report = run_audit(make_config(mechanisms.leaky_laplace_count(1.0, 0.3)))
        self.assertTrue(report["verdict"]["violation_detected"])
        explanation = report["verdict"]["explanation"]
        self.assertIn("output >= 1", explanation)  # the reproducible event
        self.assertIn("seed=123", explanation)
        self.assertIn("n_trials=4000", explanation)

    def test_deterministic_mechanism_zero_observation(self):
        report = run_audit(make_config(mechanisms.deterministic_count()))
        base = report["results"]["base"]
        self.assertEqual(base["event_count"], 0)
        # Zero observations must not be treated as a true probability of 0.
        self.assertGreater(base["ci_upper"], 0.0)
        self.assertIn("zero_observation_note", base)
        self.assertIn("NOT 0", base["zero_observation_note"])
        # The injected dataset always triggers the event: infinite ratio.
        self.assertEqual(report["ratio"]["estimate"], "inf")
        self.assertTrue(report["verdict"]["violation_detected"])

    def test_impossible_event_is_inconclusive_not_violation(self):
        config = make_config(mechanisms.laplace_count(1.0), threshold=float("inf"))
        config.event_description = "output >= inf (impossible event)"
        report = run_audit(config)
        self.assertIsNone(report["ratio"]["estimate"])
        self.assertFalse(report["verdict"]["violation_detected"])
        self.assertIn("Inconclusive", report["verdict"]["explanation"])


class TestReportContent(unittest.TestCase):
    def test_disclaimer_and_definitions_present(self):
        report = run_audit(make_config(mechanisms.laplace_count(1.0)))
        self.assertTrue(report["synthetic_data_only"])
        self.assertIn("cannot", report["disclaimer"])
        self.assertEqual(report["adjacency"], "add-one (injection)")

    def test_summary_mentions_key_fields(self):
        report = run_audit(make_config(mechanisms.laplace_count(1.0)))
        summary = format_summary(report)
        for token in ("Mechanism", "Event E", "seed=123", "Ratio", "Verdict"):
            self.assertIn(token, summary)


if __name__ == "__main__":
    unittest.main()
