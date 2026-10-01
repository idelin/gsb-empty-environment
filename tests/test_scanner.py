"""Unit tests for sensiscan. All data is synthetic (see sensiscan.synth).
These checks are correctness checks on synthetic fixtures, not a formal
privacy proof."""

from __future__ import annotations

import json
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from sensiscan import (  # noqa: E402
    DictRule,
    FieldRule,
    ScanBudget,
    Scanner,
    StreamScanner,
    WhitelistEntry,
)
from sensiscan import synth  # noqa: E402


def get_by_path(doc, path):
    node = doc
    for seg in [s for s in path.split("/") if s != ""]:
        node = node[int(seg)] if isinstance(node, list) else node[seg]
    return node


def slice_bytes(text, byte_range):
    raw = text.encode("utf-8")
    return raw[byte_range[0] : byte_range[1]].decode("utf-8")


class BaseScanTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.doc, cls.annotations, cls.whitelisted, cls.controls = (
            synth.build_reference_document()
        )
        cls.scanner = Scanner(
            dict_rules=synth.dictionary_rules(),
            field_rules=synth.field_rules(),
            whitelist=synth.whitelist(),
        )
        cls.report = cls.scanner.scan_tree(cls.doc)


class TestRecall(BaseScanTest):
    def test_status_complete(self):
        self.assertEqual(self.report.status, "complete")
        self.assertIsNone(self.report.incomplete_reason)

    def test_all_annotations_found(self):
        found = {(e.path, e.rule_id) for e in self.report.evidence}
        for ann in self.annotations:
            self.assertIn((ann.path, ann.rule_id), found, f"missing {ann}")

    def test_byte_ranges_map_back_to_planted_values(self):
        by_key = {(e.path, e.rule_id): e for e in self.report.evidence}
        for ann in self.annotations:
            ev = by_key[(ann.path, ann.rule_id)]
            source = get_by_path(self.doc, ann.path)
            self.assertEqual(slice_bytes(source, ev.byte_range), ann.value,
                             f"byte range mismatch for {ann}")

    def test_nested_arrays_scanned(self):
        found = {(e.path, e.rule_id) for e in self.report.evidence}
        self.assertIn(("/deep/0/0/0/0/email", "known-email"), found)


class TestEvidenceIntegrity(BaseScanTest):
    def test_no_duplicate_evidence(self):
        keys = [
            (e.path, e.rule_id, e.kind, e.byte_range) for e in self.report.evidence
        ]
        self.assertEqual(len(keys), len(set(keys)))

    def test_single_evidence_merges_multiple_normalization_paths(self):
        ev = next(
            e for e in self.report.evidence
            if e.path == "/users/0/ssn" and e.rule_id == "known-ssn"
        )
        self.assertGreaterEqual(len(ev.normalization_paths), 3)

    def test_variant_normalization_paths_recorded(self):
        by_path = {e.path: e for e in self.report.evidence if e.path.startswith("/notes")}
        self.assertIn("nfkc_casefold", by_path["/notes/0"].normalization_paths)
        self.assertIn("compact", by_path["/notes/1"].normalization_paths)
        self.assertIn("unescaped_compact", by_path["/notes/3"].normalization_paths)
        self.assertIn("unescaped_compact", by_path["/notes/4"].normalization_paths)


class TestFalsePositiveControls(BaseScanTest):
    def test_controls_not_flagged_by_dictionary_rules(self):
        for e in self.report.evidence:
            if e.kind != "dictionary":
                continue
            source = get_by_path(self.doc, e.path)
            raw = slice_bytes(source, e.byte_range)
            for control in self.controls:
                self.assertNotEqual(raw, control)

    def test_boundary_control_rejected(self):
        # "X123-45-6789Y" embeds the SSN with alnum boundaries: no match.
        for e in self.report.evidence:
            if e.path == "/notes/5":
                self.fail("boundary control value was flagged")


class TestRedaction(BaseScanTest):
    def test_report_does_not_replicate_sensitive_values(self):
        blob = self.report.to_json()
        planted = [a.value for a in self.annotations] + [
            synth.SSN, synth.EMAIL, synth.TOKEN,
        ]
        for value in planted:
            self.assertNotIn(value, blob)
            self.assertNotIn(json.dumps(value)[1:-1], blob)  # escaped form

    def test_report_contains_summaries(self):
        for e in self.report.evidence:
            self.assertEqual(len(e.value_sha256), 16)
            self.assertGreater(e.value_length, 0)
            self.assertTrue(e.value_shape)


class TestWhitelist(BaseScanTest):
    def test_whitelisted_path_exempted(self):
        found = {(e.path, e.rule_id) for e in self.report.evidence}
        self.assertNotIn(("/archive/0/ssn", "known-ssn"), found)
        self.assertGreater(self.report.stats["whitelisted_count"], 0)

    def test_same_field_name_elsewhere_not_exempted(self):
        found = {(e.path, e.rule_id) for e in self.report.evidence}
        self.assertIn(("/users/0/ssn", "known-ssn"), found)

    def test_wrong_rule_version_not_exempted(self):
        scanner = Scanner(
            dict_rules=synth.dictionary_rules(),
            whitelist=[WhitelistEntry("/archive/0/ssn", "known-ssn", 999)],
        )
        report = scanner.scan_tree(self.doc)
        found = {(e.path, e.rule_id) for e in report.evidence}
        self.assertIn(("/archive/0/ssn", "known-ssn"), found)

    def test_whitelist_is_exact_path_not_suffix(self):
        scanner = Scanner(
            dict_rules=synth.dictionary_rules(),
            whitelist=[WhitelistEntry("/users/0/ssn", "known-ssn", 3)],
        )
        report = scanner.scan_tree(self.doc)
        found = {(e.path, e.rule_id) for e in report.evidence}
        self.assertNotIn(("/users/0/ssn", "known-ssn"), found)
        self.assertIn(("/deep/0/0/0/0/email", "known-email"), found)


class TestBudget(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.doc = synth.build_reference_document()[0]

    def test_evidence_budget_marks_incomplete(self):
        scanner = Scanner(
            dict_rules=synth.dictionary_rules(),
            field_rules=synth.field_rules(),
            whitelist=synth.whitelist(),
            budget=ScanBudget(max_evidence=2),
        )
        report = scanner.scan_tree(self.doc)
        self.assertEqual(report.status, "incomplete")
        self.assertIn("evidence budget", report.incomplete_reason)
        self.assertLessEqual(len(report.evidence), 2)

    def test_char_budget_marks_incomplete(self):
        scanner = Scanner(
            dict_rules=synth.dictionary_rules(),
            budget=ScanBudget(max_chars=10),
        )
        report = scanner.scan_tree(self.doc)
        self.assertEqual(report.status, "incomplete")
        self.assertIn("character budget", report.incomplete_reason)

    def test_generous_budget_completes(self):
        scanner = Scanner(
            dict_rules=synth.dictionary_rules(),
            budget=ScanBudget(max_evidence=1000, max_chars=10**6),
        )
        report = scanner.scan_tree(self.doc)
        self.assertEqual(report.status, "complete")


class TestStreaming(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.text, cls.chunks, cls.expected = synth.build_stream_sample()
        cls.scanner = Scanner(dict_rules=synth.dictionary_rules())

    def run_stream(self):
        stream = StreamScanner(self.scanner)
        for chunk in self.chunks:
            stream.feed(chunk)
        return stream.finish()

    def test_cross_chunk_matches_found(self):
        report = self.run_stream()
        self.assertEqual(report.status, "complete")
        found = {e.rule_id for e in report.evidence}
        self.assertEqual(found, set(self.expected))

    def test_secrets_actually_cross_chunk_boundaries(self):
        for secret in self.expected.values():
            start = self.text.find(secret)
            end = start + len(secret)
            crosses = any(
                start < boundary < end
                for boundary in range(7, len(self.text), 7)
            )
            self.assertTrue(crosses, f"{secret!r} does not cross a chunk boundary")

    def test_byte_ranges_map_to_stream(self):
        report = self.run_stream()
        for e in report.evidence:
            self.assertEqual(slice_bytes(self.text, e.byte_range),
                             self.expected[e.rule_id])

    def test_streaming_matches_batch(self):
        stream_report = self.run_stream()
        batch_report = self.scanner.scan_text(self.text, path="/<stream>")
        key = lambda e: (e.rule_id, e.byte_range)  # noqa: E731
        self.assertEqual(
            sorted(map(key, stream_report.evidence)),
            sorted(map(key, batch_report.evidence)),
        )

    def test_no_duplicate_evidence_across_chunks(self):
        report = self.run_stream()
        keys = [(e.rule_id, e.byte_range) for e in report.evidence]
        self.assertEqual(len(keys), len(set(keys)))


class TestFieldRules(unittest.TestCase):
    def test_field_rule_flags_unknown_value(self):
        scanner = Scanner(field_rules=synth.field_rules())
        report = scanner.scan_tree({"users": [{"ssn": "000-00-0000"}]})
        self.assertEqual(len(report.evidence), 1)
        self.assertEqual(report.evidence[0].kind, "field")
        self.assertEqual(report.evidence[0].path, "/users/0/ssn")

    def test_field_rule_pattern_does_not_overmatch(self):
        scanner = Scanner(field_rules=[FieldRule("f", 1, "/users/*/ssn")])
        report = scanner.scan_tree({"users": [{"deep": {"ssn": "x"}}, {"ssnx": "y"}]})
        self.assertEqual(report.evidence, [])


if __name__ == "__main__":
    unittest.main()
