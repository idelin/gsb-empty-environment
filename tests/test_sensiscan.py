"""Unit tests for sensiscan.  Synthetic data only."""
import json
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

import reference_corpus as corpus
from sensiscan.escapes import decode_escapes
from sensiscan.normalize import NORM_PATHS, normalize_entry
from sensiscan.scanner import Budget, Rule, WhitelistEntry, scan_document
from sensiscan.stream import StreamScanner


def scan_value(value, entries, chunk_size=1 << 20):
    patterns = [
        (("r", i), {name: normalize_entry(name, e) for name in NORM_PATHS})
        for i, e in enumerate(entries)
    ]
    scanner = StreamScanner(patterns)
    for i in range(0, len(value), chunk_size):
        scanner.feed(value[i : i + chunk_size])
    scanner.finalize()
    return scanner.matches


class TestEscapes(unittest.TestCase):
    def test_unicode_escape_and_surrogate_pair(self):
        text, spans = decode_escapes("\\uD83D\\uDE00")
        self.assertEqual(text, "\U0001F600")
        self.assertEqual(spans, [(0, 12)])

    def test_percent_encoded_utf8(self):
        text, spans = decode_escapes("%e7%94%a8")
        self.assertEqual(text, "用")
        self.assertEqual(spans, [(0, 9)])

    def test_named_entity(self):
        text, spans = decode_escapes("&amp;")
        self.assertEqual(text, "&")
        self.assertEqual(spans, [(0, 5)])

    def test_invalid_sequences_pass_through_literally(self):
        self.assertEqual(decode_escapes("%zz")[0], "%zz")
        self.assertEqual(decode_escapes("abc%4")[0], "abc%4")
        self.assertEqual(decode_escapes("\\u12")[0], "\\u12")


class TestStreaming(unittest.TestCase):
    def test_cross_chunk_plain_match(self):
        value = "x" * 37 + "admin" + "y" * 37
        matches = scan_value(value, ["admin"], chunk_size=7)
        self.assertEqual(len(matches), 1)
        _, start, end, via = matches[0]
        self.assertEqual((start, end), (37, 42))
        self.assertEqual(via, sorted(NORM_PATHS))

    def test_escape_split_across_chunks(self):
        value = "alice.w%40example.com"
        for chunk_size in (1, 2, 3, 5, 7, 64):
            with self.subTest(chunk_size=chunk_size):
                matches = scan_value(value, ["alice.w@example.com"], chunk_size)
                self.assertEqual(len(matches), 1)
                _, start, end, _ = matches[0]
                self.assertEqual((start, end), (0, len(value)))

    def test_chunk_size_invariance(self):
        value = "x\\u0041dmin y alice.w%40example.com z"
        entries = ["admin", "alice.w@example.com"]
        expected = {
            (1, 1 + len("\\u0041dmin")),
            (value.index("alice.w%40"), value.index("alice.w%40") + 21),
        }
        for chunk_size in (1, 2, 3, 7, 13, 1000):
            with self.subTest(chunk_size=chunk_size):
                matches = scan_value(value, entries, chunk_size)
                self.assertEqual({(s, e) for _, s, e, _ in matches}, expected)


class TestNormalizationVariants(unittest.TestCase):
    ENTRIES = ["admin", "alice.w@example.com", "13800000000", "alice zhang"]
    VARIANTS = {
        "ＡＤＭＩＮ": "admin",                       # full-width
        "AdMin": "admin",                           # case
        "a d\u200bmin": "admin",                    # spaces + zero-width
        "ALICE.W@EXAMPLE.COM": "alice.w@example.com",
        "ａｌｉｃｅ．ｗ＠ｅｘａｍｐｌｅ．ｃｏｍ": "alice.w@example.com",
        "alice.w%40example.com": "alice.w@example.com",
        "&#97;lice.w@example.com": "alice.w@example.com",
        "\\u0061lice.w@example.com": "alice.w@example.com",
        "138-0000-0000": "13800000000",
        "+86 138 0000 0000": "13800000000",
        "Alice  Zhang": "alice zhang",
        "Alice\u200b Zhang": "alice zhang",
    }

    def test_each_variant_matches_exactly_once(self):
        for variant, entry in self.VARIANTS.items():
            with self.subTest(variant=variant):
                matches = scan_value(variant, self.ENTRIES)
                self.assertEqual(len(matches), 1)
                (pid, start, end, _), = matches
                self.assertEqual(pid, ("r", self.ENTRIES.index(entry)))
                # "+86 ..." carries a prefix that is not part of the entry
                want = (4, len(variant)) if variant.startswith("+86") else (0, len(variant))
                self.assertEqual((start, end), want)

    def test_near_misses_do_not_match(self):
        near_misses = [
            "adnin",
            "admi",
            "alice.w@example.org",
            "alice.wx@example.com",
            "13800000001",
            "1380000000",
            "alice zhou",
            "alice  zhou",
        ]
        for value in near_misses:
            with self.subTest(value=value):
                self.assertEqual(scan_value(value, self.ENTRIES), [])


class TestCorpusScan(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.report = scan_document(
            corpus.DOC, corpus.RULES, corpus.WHITELIST, chunk_size=8
        )

    def test_scan_completes(self):
        self.assertTrue(self.report.complete)
        self.assertEqual(self.report.incomplete_reasons, [])

    def test_recall_and_precision_against_annotations(self):
        found = {}
        for ev in self.report.evidence:
            key = (ev.path, ev.rule_id)
            found[key] = found.get(key, 0) + 1
        expected = {(e["path"], e["rule_id"]): e["count"] for e in corpus.EXPECTED}
        self.assertEqual(found, expected)  # no misses, no extras

    def test_nested_arrays_are_scanned(self):
        paths = {ev.path for ev in self.report.evidence}
        self.assertIn("/user/phones/0/0", paths)
        self.assertIn("/user/phones/1/0", paths)

    def test_whitelist_binds_exact_path_and_version(self):
        paths = {ev.path for ev in self.report.evidence}
        self.assertNotIn("/public_email", paths)          # exact match: exempted
        self.assertEqual(self.report.suppressed, corpus.EXPECTED_SUPPRESSED)
        self.assertIn("/backup/email", paths)             # same key name, other path
        self.assertIn("/legacy/email", paths)             # stale whitelist version

    def test_no_duplicate_evidence(self):
        keys = [
            (ev.path, ev.rule_id, ev.byte_start, ev.byte_end)
            for ev in self.report.evidence
        ]
        self.assertEqual(len(keys), len(set(keys)))

    def test_controls_produce_no_false_positives(self):
        for ev in self.report.evidence:
            for prefix in corpus.CONTROL_PREFIXES:
                self.assertFalse(ev.path.startswith(prefix), ev.path)

    def test_report_does_not_leak_raw_values(self):
        blob_plain = json.dumps(self.report.to_dict(), ensure_ascii=False)
        blob_ascii = json.dumps(self.report.to_dict(), ensure_ascii=True)
        for secret in corpus.SECRETS:
            with self.subTest(secret=secret):
                self.assertNotIn(secret, blob_plain)
                self.assertNotIn(secret, blob_ascii)

    def test_byte_ranges_map_back_to_raw_text(self):
        value = corpus.DOC["notes"]
        raw = value.encode("utf-8")
        pieces = sorted(
            raw[ev.byte_start : ev.byte_end]
            for ev in self.report.evidence
            if ev.path == "/notes"
        )
        self.assertEqual(
            pieces,
            sorted([b"alice.w%40example.com", b"&#97;lice.w@example.com"]),
        )


class TestByteMapping(unittest.TestCase):
    def test_multibyte_prefix_byte_range(self):
        doc = {"k": "用户:alice.w@example.com。"}
        rules = [Rule("r", "v1", ("*",), ("alice.w@example.com",))]
        report = scan_document(doc, rules)
        self.assertEqual(len(report.evidence), 1)
        ev = report.evidence[0]
        raw = doc["k"].encode("utf-8")
        self.assertEqual(raw[ev.byte_start : ev.byte_end], b"alice.w@example.com")
        self.assertEqual(ev.byte_start, len("用户:".encode("utf-8")))


class TestBudget(unittest.TestCase):
    def test_evidence_budget_marks_incomplete(self):
        report = scan_document(
            corpus.DOC, corpus.RULES, corpus.WHITELIST, Budget(max_evidence=3)
        )
        self.assertFalse(report.complete)
        self.assertEqual(len(report.evidence), 3)
        self.assertTrue(any("evidence budget" in r for r in report.incomplete_reasons))

    def test_char_budget_marks_incomplete(self):
        report = scan_document(corpus.DOC, corpus.RULES, budget=Budget(max_chars=20))
        self.assertFalse(report.complete)
        self.assertTrue(any("char budget" in r for r in report.incomplete_reasons))

    def test_string_budget_marks_incomplete(self):
        report = scan_document(corpus.DOC, corpus.RULES, budget=Budget(max_strings=2))
        self.assertFalse(report.complete)
        self.assertTrue(any("string budget" in r for r in report.incomplete_reasons))


class TestWhitelistUnit(unittest.TestCase):
    RULES = [Rule("known-email", "v1.0", ("*",), ("alice.w@example.com",))]
    DOC = {"a": {"email": "alice.w@example.com"}, "b": {"email": "alice.w@example.com"}}

    def test_only_exact_path_exempted(self):
        whitelist = [WhitelistEntry("/a/email", "known-email", "v1.0")]
        report = scan_document(self.DOC, self.RULES, whitelist)
        self.assertEqual([ev.path for ev in report.evidence], ["/b/email"])
        self.assertEqual(report.suppressed, 1)

    def test_version_mismatch_not_exempted(self):
        whitelist = [WhitelistEntry("/a/email", "known-email", "v0.9")]
        report = scan_document(self.DOC, self.RULES, whitelist)
        self.assertEqual(len(report.evidence), 2)
        self.assertEqual(report.suppressed, 0)

    def test_rule_id_mismatch_not_exempted(self):
        whitelist = [WhitelistEntry("/a/email", "other-rule", "v1.0")]
        report = scan_document(self.DOC, self.RULES, whitelist)
        self.assertEqual(len(report.evidence), 2)


if __name__ == "__main__":
    unittest.main()
