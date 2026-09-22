from __future__ import annotations

import io
import json
import os
import random
import shutil
import stat
import zipfile
import unittest

from reproduce_compare.archive import Budget
from reproduce_compare.compare import (
    DIFFERENT,
    EQUAL,
    EQUIVALENT,
    UNDETERMINABLE,
)
from reproduce_compare.evidence import build_bundle
from reproduce_compare.rules import RuleError, load_rules
from reproduce_compare.verify import verify_bundle

from ._helpers import Workspace, make_zip, run_case

SIMPLE_MEMBER = ("doc.txt", b"payload", (1980, 1, 1, 0, 0, 0), 0o644, None)


def zip_bytes(members, raw=False):
    if raw:
        return members
    return make_zip(members)


def flip_byte(data: bytes, index: int) -> bytes:
    value = data[index] ^ 0xFF
    return data[:index] + bytes([value]) + data[index + 1:]


class BasicTreeTests(unittest.TestCase):
    def setUp(self):
        self.ws = Workspace()

    def tearDown(self):
        self.ws.cleanup()

    def test_identical_trees_equal(self):
        self.ws.write_old("a/b.txt", b"hello")
        self.ws.write_new("a/b.txt", b"hello")
        _, result, _, verdict = run_case(self.ws)
        self.assertEqual(result.status, EQUAL)
        self.assertTrue(verdict.ok)

    def test_content_change_different(self):
        self.ws.write_old("a.txt", b"one")
        self.ws.write_new("a.txt", b"two")
        _, result, _, verdict = run_case(self.ws)
        self.assertEqual(result.status, DIFFERENT)
        self.assertTrue(any(d.aspect == "content" for d in result.differences))
        self.assertTrue(verdict.ok)

    def test_permission_change_is_substantive(self):
        self.ws.write_old("a.sh", b"echo x\n")
        self.ws.write_new("a.sh", b"echo x\n")
        self.ws.chmod_old("a.sh", 0o755)
        self.ws.chmod_new("a.sh", 0o644)
        _, result, _, verdict = run_case(self.ws)
        self.assertEqual(result.status, DIFFERENT)
        self.assertTrue(any(d.aspect == "mode" for d in result.differences))
        self.assertTrue(verdict.ok)

    def test_symlink_target_change_is_substantive(self):
        self.ws.write_old("target_a", b"x")
        self.ws.write_new("target_a", b"x")
        self.ws.write_new("target_b", b"x")
        self.ws.symlink_old("link", "target_a")
        self.ws.symlink_new("link", "target_b")
        _, result, _, verdict = run_case(self.ws)
        self.assertEqual(result.status, DIFFERENT)
        self.assertTrue(any(d.aspect == "symlink-target" for d in result.differences))
        self.assertTrue(verdict.ok)

    def test_presence_difference(self):
        self.ws.write_old("only_here.txt", b"x")
        _, result, _, _ = run_case(self.ws)
        self.assertEqual(result.status, DIFFERENT)


class ZipTimestampTests(unittest.TestCase):
    def setUp(self):
        self.ws = Workspace()

    def tearDown(self):
        self.ws.cleanup()

    def test_only_member_timestamps_differ_equivalent(self):
        old_zip = make_zip([("a.txt", b"same", (2019, 1, 1, 0, 0, 0), 0o644, None)])
        new_zip = make_zip([("a.txt", b"same", (2024, 6, 7, 8, 9, 10), 0o644, None)])
        self.ws.write_old("a.zip", old_zip)
        self.ws.write_new("a.zip", new_zip)
        _, result, bundle, verdict = run_case(self.ws)
        self.assertEqual(result.status, EQUIVALENT)
        self.assertEqual(len(result.ignored), 1)
        rec = result.ignored[0]
        self.assertEqual(rec.rule_id, "builtin:zip-timestamp:v1")
        self.assertEqual(rec.old_value["date_time"], [2019, 1, 1, 0, 0, 0])
        self.assertEqual(rec.new_value["date_time"], [2024, 6, 7, 8, 9, 10])
        self.assertTrue(verdict.ok, verdict.problems)

    def test_payload_pseudo_timestamp_is_not_ignored(self):
        # The bytes that look like a date live in *payload*, not in parser
        # metadata: they must be compared verbatim.
        old_zip = make_zip([("a.txt", b"2019-01-01T00:00:00Z payload",
                            (1980, 1, 1, 0, 0, 0), 0o644, None)])
        new_zip = make_zip([("a.txt", b"2024-06-07T08:09:10Z payload",
                            (1980, 1, 1, 0, 0, 0), 0o644, None)])
        self.ws.write_old("a.zip", old_zip)
        self.ws.write_new("a.zip", new_zip)
        _, result, _, verdict = run_case(self.ws)
        self.assertEqual(result.status, DIFFERENT)
        self.assertTrue(any(d.aspect == "content" for d in result.differences))
        self.assertEqual(result.ignored, [])
        self.assertTrue(verdict.ok)

    def test_protected_payload_byte_flip_never_ignored(self):
        payload = bytes((i * 31 + 7) % 256 for i in range(4096))
        buf = io.BytesIO()
        info = zipfile.ZipInfo("data.bin", date_time=(1980, 1, 1, 0, 0, 0))
        info.create_system = 3
        info.external_attr = (stat.S_IFREG | 0o644) << 16
        with zipfile.ZipFile(buf, "w", zipfile.ZIP_STORED) as zf:
            zf.writestr(info, payload)
        base = buf.getvalue()
        # Local header is 30 bytes plus the filename; stored payload follows.
        index = 30 + len("data.bin") + 100
        tampered = flip_byte(base, index)
        self.ws.write_old("a.zip", base)
        self.ws.write_new("a.zip", tampered)
        _, result, _, _ = run_case(self.ws)
        # A payload byte flip either changes decompressed content or breaks CRC;
        # in neither case may it be reported as an allowed equivalence.
        self.assertIn(result.status, (DIFFERENT, UNDETERMINABLE))
        self.assertNotEqual(result.status, EQUIVALENT)

    def test_raw_identical_zip_equal_despite_timestamps_policy(self):
        data = make_zip([("a.txt", b"x", (2020, 3, 4, 5, 6, 7), 0o644, None)])
        self.ws.write_old("a.zip", data)
        self.ws.write_new("a.zip", data)
        _, result, _, verdict = run_case(self.ws)
        self.assertEqual(result.status, EQUAL)
        self.assertEqual(result.ignored, [])
        self.assertTrue(verdict.ok)


class NestedZipJsonTests(unittest.TestCase):
    def setUp(self):
        self.ws = Workspace()

    def tearDown(self):
        self.ws.cleanup()

    def _nested(self, doc, ts=(1980, 1, 1, 0, 0, 0)):
        inner = make_zip([("doc.json", json.dumps(doc, separators=(",", ":")).encode(),
                           ts, 0o644, None)])
        return make_zip([("inner.zip", inner, (1980, 1, 1, 0, 0, 0), 0o644, None),
                         ("readme", b"stable", (1980, 1, 1, 0, 0, 0), 0o644, None)])

    def test_nested_json_big_integer_preserved(self):
        big = 10 ** 40 + 123456789
        old_doc = {"id": big, "labels": ["a", "b"], "built_at": "2020-01-01T00:00:00Z"}
        new_doc = {"id": big, "labels": ["a", "b"], "built_at": "2025-09-22T00:00:00Z"}
        self.ws.write_old("outer.zip", self._nested(old_doc))
        self.ws.write_new("outer.zip", self._nested(new_doc))
        rules_doc = {"json_key_ignores": [{
            "rule_id": "built-at",
            "target": "outer.zip!/inner.zip!/doc.json",
            "key_path": ["built_at"],
            "expected_type": "string",
        }]}
        _, result, bundle, verdict = run_case(self.ws, rules_doc)
        self.assertEqual(result.status, EQUIVALENT)
        rec = next(r for r in result.ignored if r.aspect == "json-key")
        self.assertEqual(rec.field_path, "outer.zip!/inner.zip!/doc.json#built_at")
        self.assertIn("built-at", rec.rule_id)
        self.assertTrue(verdict.ok, verdict.problems)

    def test_big_integer_one_digit_off_is_different(self):
        old_doc = {"id": 10 ** 40}
        new_doc = {"id": 10 ** 40 + 1}
        self.ws.write_old("outer.zip", self._nested(old_doc))
        self.ws.write_new("outer.zip", self._nested(new_doc))
        _, result, _, _ = run_case(self.ws)
        self.assertEqual(result.status, DIFFERENT)

    def test_array_order_is_significant(self):
        old_doc = {"seq": [1, 2, 3]}
        new_doc = {"seq": [3, 2, 1]}
        self.ws.write_old("outer.zip", self._nested(old_doc))
        self.ws.write_new("outer.zip", self._nested(new_doc))
        _, result, _, _ = run_case(self.ws)
        self.assertEqual(result.status, DIFFERENT)

    def test_ignored_field_change_keeps_both_evidence_and_equivalence(self):
        old_doc = {"keep": 1, "meta": {"built": "t1"}}
        new_doc = {"keep": 1, "meta": {"built": "t2"}}
        self.ws.write_old("outer.zip", self._nested(old_doc))
        self.ws.write_new("outer.zip", self._nested(new_doc))
        rules_doc = {"json_key_ignores": [{
            "rule_id": "built",
            "target": "outer.zip!/inner.zip!/doc.json",
            "key_path": ["meta", "built"],
            "expected_type": "string",
        }]}
        ruleset, result, bundle, verdict = run_case(self.ws, rules_doc)
        self.assertEqual(result.status, EQUIVALENT)
        rec = result.ignored[0]
        self.assertEqual(rec.old_value, "t1")
        self.assertEqual(rec.new_value, "t2")
        self.assertNotEqual(rec.old_digest, rec.new_digest)
        self.assertTrue(verdict.ok)

    def test_nested_member_timestamp_only_is_equivalent(self):
        big = 7
        old_outer = self._nested({"id": big}, ts=(2011, 1, 1, 0, 0, 0))
        new_outer = self._nested({"id": big}, ts=(2022, 2, 2, 2, 2, 2))
        self.ws.write_old("outer.zip", old_outer)
        self.ws.write_new("outer.zip", new_outer)
        _, result, _, verdict = run_case(self.ws)
        self.assertEqual(result.status, EQUIVALENT)
        self.assertTrue(any(r.aspect == "zip-metadata" for r in result.ignored))
        self.assertTrue(verdict.ok)


def _zip_with_duplicate_member() -> bytes:
    import warnings
    buf = io.BytesIO()
    with warnings.catch_warnings():
        warnings.simplefilter("ignore")
        with zipfile.ZipFile(buf, "w", zipfile.ZIP_STORED) as zf:
            zf.writestr("dup.txt", b"first")
            zf.writestr("dup.txt", b"second")
            zf.writestr("other.txt", b"x")
    return buf.getvalue()




def _zip_with_traversal() -> bytes:
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w", zipfile.ZIP_STORED) as zf:
        zf.writestr("../escape.txt", b"x")
        zf.writestr("ok.txt", b"y")
    return buf.getvalue()


def _zip_with_backslash() -> bytes:
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w", zipfile.ZIP_STORED) as zf:
        zf.writestr("..\\..\\escape.txt", b"x")
    return buf.getvalue()


class MalformedArchiveTests(unittest.TestCase):
    def setUp(self):
        self.ws = Workspace()

    def tearDown(self):
        self.ws.cleanup()

    def test_duplicate_member_is_undeterminable_even_if_both_sides_same(self):
        data = _zip_with_duplicate_member()
        self.ws.write_old("a.zip", data)
        self.ws.write_new("a.zip", data)
        _, result, _, verdict = run_case(self.ws)
        self.assertEqual(result.status, UNDETERMINABLE)
        self.assertTrue(any(u.reason == "duplicate-zip-member" for u in result.unknown))
        self.assertTrue(verdict.ok)

    def test_path_traversal_member_undeterminable(self):
        data = _zip_with_traversal()
        self.ws.write_old("a.zip", data)
        self.ws.write_new("a.zip", data)
        _, result, _, _ = run_case(self.ws)
        self.assertEqual(result.status, UNDETERMINABLE)
        self.assertTrue(any(u.reason == "path-traversal" for u in result.unknown))

    def test_backslash_traversal_member_undeterminable(self):
        data = _zip_with_backslash()
        self.ws.write_old("a.zip", data)
        self.ws.write_new("a.zip", data)
        _, result, _, _ = run_case(self.ws)
        self.assertEqual(result.status, UNDETERMINABLE)
        self.assertTrue(any(u.reason == "path-traversal" for u in result.unknown))

    def test_truncated_archive_undeterminable(self):
        good = make_zip([("a.bin", b"abcdef" * 200, (1980, 1, 1, 0, 0, 0), 0o644, None)])
        # Truncate before the central directory: parser cannot open the archive.
        broken = good[: int(len(good) * 0.4)]
        self.ws.write_old("a.zip", good)
        self.ws.write_new("a.zip", broken)
        _, result, _, _ = run_case(self.ws)
        self.assertEqual(result.status, UNDETERMINABLE)
        self.assertTrue(any(u.reason == "corrupt-archive" for u in result.unknown))

    def test_payload_crc_corruption_undeterminable(self):
        good = make_zip([("a.bin", os.urandom(2048), (1980, 1, 1, 0, 0, 0), 0o644, None)])
        # Flip a byte inside the compressed local-file data (not the header).
        broken = flip_byte(good, 60)
        self.ws.write_old("a.zip", good)
        self.ws.write_new("a.zip", broken)
        _, result, _, _ = run_case(self.ws)
        self.assertIn(result.status, (DIFFERENT, UNDETERMINABLE))
        self.assertNotEqual(result.status, EQUIVALENT)

    def test_decompression_byte_budget_exhausted(self):
        big_payload = b"A" * 200_000
        data = make_zip([("big.bin", big_payload, (1980, 1, 1, 0, 0, 0), 0o644, None)])
        self.ws.write_old("a.zip", data)
        self.ws.write_new("a.zip", data)
        budget = Budget(max_depth=8, max_uncompressed_bytes=10_000, max_entries=1000)
        ruleset = load_rules(None)
        from reproduce_compare.compare import compare_trees
        result = compare_trees(self.ws.old, self.ws.new, ruleset, budget)
        self.assertEqual(result.status, UNDETERMINABLE)
        self.assertTrue(any(u.reason == "budget-exhausted" for u in result.unknown))

    def test_entry_count_budget_exhausted(self):
        members = [(f"f{i}.txt", b"x", (1980, 1, 1, 0, 0, 0), 0o644, None)
                   for i in range(50)]
        data = make_zip(members)
        self.ws.write_old("a.zip", data)
        self.ws.write_new("a.zip", data)
        budget = Budget(max_depth=8, max_uncompressed_bytes=10**9, max_entries=10)
        from reproduce_compare.compare import compare_trees
        result = compare_trees(self.ws.old, self.ws.new, load_rules(None), budget)
        self.assertEqual(result.status, UNDETERMINABLE)
        self.assertTrue(any(u.reason == "budget-exhausted" for u in result.unknown))

    def test_nesting_depth_budget_exhausted(self):
        payload = b"deep"
        blob = payload
        for _ in range(5):
            blob = make_zip([("level.zip", blob, (1980, 1, 1, 0, 0, 0), 0o644, None)])
        self.ws.write_old("a.zip", blob)
        self.ws.write_new("a.zip", blob)
        budget = Budget(max_depth=2, max_uncompressed_bytes=10**9, max_entries=1000)
        from reproduce_compare.compare import compare_trees
        result = compare_trees(self.ws.old, self.ws.new, load_rules(None), budget)
        self.assertEqual(result.status, UNDETERMINABLE)
        self.assertTrue(any(u.reason == "budget-exhausted" for u in result.unknown))


class JsonRuleTests(unittest.TestCase):
    def setUp(self):
        self.ws = Workspace()

    def tearDown(self):
        self.ws.cleanup()

    def test_duplicate_json_keys_rejected(self):
        self.ws.write_old("a.json", b'{"x":1}')
        self.ws.write_new("a.json", b'{"x":1,"x":2}')
        rules_doc = {"json_key_ignores": [{
            "rule_id": "r", "target": "a.json", "key_path": ["x"]}]}
        _, result, _, verdict = run_case(self.ws, rules_doc)
        self.assertEqual(result.status, UNDETERMINABLE)
        self.assertTrue(any(u.reason == "json-duplicate-key" for u in result.unknown))
        self.assertTrue(verdict.ok)

    def test_overlapping_rules_rejected(self):
        with self.assertRaises(RuleError):
            load_rules({"json_key_ignores": [
                {"rule_id": "r1", "target": "a.json", "key_path": ["meta"]},
                {"rule_id": "r2", "target": "a.json", "key_path": ["meta", "ts"]},
            ]})

    def test_duplicate_rule_definition_rejected(self):
        with self.assertRaises(RuleError):
            load_rules({"json_key_ignores": [
                {"rule_id": "r1", "target": "a.json", "key_path": ["x"]},
                {"rule_id": "r2", "target": "a.json", "key_path": ["x"]},
            ]})

    def test_target_type_mismatch_is_undeterminable(self):
        self.ws.write_old("a.json", b'{"x": 5}')
        self.ws.write_new("a.json", b'{"x": 5}')
        rules_doc = {"json_key_ignores": [{
            "rule_id": "r", "target": "a.json", "key_path": ["x"],
            "expected_type": "string"}]}
        _, result, _, verdict = run_case(self.ws, rules_doc)
        self.assertEqual(result.status, UNDETERMINABLE)
        self.assertTrue(any(u.reason == "rule-target-type-mismatch" for u in result.unknown))
        self.assertTrue(verdict.ok)

    def test_rule_target_missing_key_undeterminable(self):
        self.ws.write_old("a.json", b'{"x": 1}')
        self.ws.write_new("a.json", b'{"x": 1}')
        rules_doc = {"json_key_ignores": [{
            "rule_id": "r", "target": "a.json", "key_path": ["nope"]}]}
        _, result, _, _ = run_case(self.ws, rules_doc)
        self.assertEqual(result.status, UNDETERMINABLE)

    def test_rule_target_file_missing_undeterminable(self):
        rules_doc = {"json_key_ignores": [{
            "rule_id": "r", "target": "ghost.json", "key_path": ["x"]}]}
        _, result, _, _ = run_case(self.ws, rules_doc)
        self.assertEqual(result.status, UNDETERMINABLE)
        self.assertTrue(any(u.reason == "rule-target-missing" for u in result.unknown))

    def test_decimal_literal_precision_preserved(self):
        self.ws.write_old("a.json", b'{"v": 0.1, "ts": 1}')
        self.ws.write_new("a.json", b'{"v": 0.1, "ts": 2}')
        rules_doc = {"json_key_ignores": [{
            "rule_id": "r", "target": "a.json", "key_path": ["ts"],
            "expected_type": "integer"}]}
        _, result, _, verdict = run_case(self.ws, rules_doc)
        self.assertEqual(result.status, EQUIVALENT)
        self.assertTrue(verdict.ok)

    def test_decimal_value_change_is_different(self):
        self.ws.write_old("a.json", b'{"v": 0.1}')
        self.ws.write_new("a.json", b'{"v": 0.30000000000000004}')
        _, result, _, _ = run_case(self.ws)
        self.assertEqual(result.status, DIFFERENT)


class ZipSymlinkMemberTests(unittest.TestCase):
    def setUp(self):
        self.ws = Workspace()

    def tearDown(self):
        self.ws.cleanup()

    def test_zip_symlink_target_change(self):
        old = make_zip([("lnk", b"", (1980, 1, 1, 0, 0, 0), 0o777, "target-a")])
        new = make_zip([("lnk", b"", (1980, 1, 1, 0, 0, 0), 0o777, "target-b")])
        self.ws.write_old("a.zip", old)
        self.ws.write_new("a.zip", new)
        _, result, _, verdict = run_case(self.ws)
        self.assertEqual(result.status, DIFFERENT)
        self.assertTrue(any(d.aspect == "symlink-target" for d in result.differences))
        self.assertTrue(verdict.ok)


class RandomMutationTests(unittest.TestCase):
    """Synthetic file: mutate a random protected byte vs an ignorable field."""

    def setUp(self):
        self.ws = Workspace()
        rng = random.Random(99)
        doc = {
            "id": 10 ** 38,
            "payload": "".join(rng.choice("abcdef0123456789") for _ in range(256)),
            "nested": {"values": [rng.randrange(10 ** 12) for _ in range(12)]},
            "build": {"timestamp": "2020-01-01T00:00:00Z"},
        }
        self.doc = doc
        self.rules_doc = {"json_key_ignores": [{
            "rule_id": "build-ts",
            "target": "synthetic.json",
            "key_path": ["build", "timestamp"],
            "expected_type": "string",
        }]}
        self.original = json.dumps(doc, indent=2, sort_keys=True).encode("utf-8")

    def tearDown(self):
        self.ws.cleanup()

    def test_random_protected_byte_flip_is_different(self):
        rng = random.Random(7)
        # Change a character inside the protected random payload string to a
        # different hex character. JSON still parses; the protected value changes.
        marker = b'"payload": "'
        start = self.original.index(marker) + len(marker)
        idx = start + rng.randrange(200)
        original_char = self.original[idx:idx + 1]
        replacement = b"0" if original_char != b"0" else b"1"
        mutated = self.original[:idx] + replacement + self.original[idx + 1:]
        self.assertNotEqual(mutated, self.original)
        self.ws.write_old("synthetic.json", self.original)
        self.ws.write_new("synthetic.json", mutated)
        _, result, _, verdict = run_case(self.ws, self.rules_doc)
        self.assertEqual(result.status, DIFFERENT)
        self.assertTrue(verdict.ok)

    def test_random_ignorable_field_change_is_equivalent_with_evidence(self):
        rng = random.Random(13)
        doc = json.loads(self.original.decode("utf-8"))
        stamp = f"2026-09-{20 + rng.randrange(2):02d}T{rng.randrange(24):02d}:00:00Z"
        doc["build"]["timestamp"] = stamp
        mutated = json.dumps(doc, indent=2, sort_keys=True).encode("utf-8")
        self.ws.write_old("synthetic.json", self.original)
        self.ws.write_new("synthetic.json", mutated)
        _, result, bundle, verdict = run_case(self.ws, self.rules_doc)
        self.assertEqual(result.status, EQUIVALENT)
        rec = next(r for r in result.ignored if r.rule_id == "build-ts")
        self.assertEqual(rec.new_value, stamp)
        self.assertNotEqual(rec.old_digest, rec.new_digest)
        # Evidence retains the original raw digests of both inputs.
        self.assertNotEqual(bundle["inputs"]["old_manifest_digest"],
                            bundle["inputs"]["new_manifest_digest"])
        self.assertTrue(verdict.ok, verdict.problems)


class EvidenceTamperTests(unittest.TestCase):
    def setUp(self):
        self.ws = Workspace()
        self.ws.write_old("a.txt", b"one")
        self.ws.write_new("a.txt", b"two")

    def tearDown(self):
        self.ws.cleanup()

    def test_verifier_rejects_forged_equivalence(self):
        from reproduce_compare.compare import compare_trees
        ruleset = load_rules(None)
        honest = compare_trees(self.ws.old, self.ws.new, ruleset)
        bundle = build_bundle(honest, ruleset)
        # Forge the conclusion while leaving raw inputs unchanged.
        bundle["conclusion"]["status"] = "equal"
        verdict = verify_bundle(bundle, self.ws.old, self.ws.new)
        self.assertFalse(verdict.ok)
        self.assertTrue(any("status mismatch" in p for p in verdict.problems))

    def test_verifier_rejects_forged_ignore_value_digest(self):
        self.ws.write_old("j.json", b'{"keep": 1, "ts": 1}')
        self.ws.write_new("j.json", b'{"keep": 1, "ts": 2}')
        rules_doc = {"json_key_ignores": [{
            "rule_id": "r", "target": "j.json", "key_path": ["ts"]}]}
        _, result, bundle, _ = run_case(self.ws, rules_doc)
        # The tree also contains the differing a.txt from setUp; the j.json
        # ignore record must still be present with genuinely differing values.
        rec = next(r for r in result.ignored if r.field_path == "j.json#ts")
        self.assertEqual((rec.old_value, rec.new_value), (1, 2))
        bundle["conclusion"]["ignored"][0]["old_digest"] = "0" * 64
        verdict = verify_bundle(bundle, self.ws.old, self.ws.new)
        self.assertFalse(verdict.ok)
        self.assertTrue(any("digest inconsistent" in problem
                            for problem in verdict.problems))


    def test_verifier_rejects_hidden_difference(self):
        # Evidence claims "equal" with no differences; verifier must recompute
        # the differing a.txt from raw inputs and reject the bundle.
        _, result, bundle, _ = run_case(self.ws)
        self.assertEqual(result.status, DIFFERENT)
        bundle["conclusion"]["status"] = "equal"
        bundle["conclusion"]["differences"] = []
        verdict = verify_bundle(bundle, self.ws.old, self.ws.new)
        self.assertFalse(verdict.ok)
        self.assertTrue(any("differences" in problem for problem in verdict.problems))

    def test_verifier_rejects_tampered_rule_document(self):
        from reproduce_compare.rules import rules_to_bytes
        from reproduce_compare.digest import digest_bytes
        self.ws.write_old("j.json", b'{"keep": 1, "ts": 1}')
        self.ws.write_new("j.json", b'{"keep": 1, "ts": 2}')
        rules_doc = {"json_key_ignores": [{
            "rule_id": "r", "target": "j.json", "key_path": ["ts"]}]}
        _, result, bundle, _ = run_case(self.ws, rules_doc)
        # Forge the rule document (e.g. claim timestamps must not be ignored)
        # without updating its stored digest; verification must fail.
        bundle["rules"]["document"]["ignore_zip_timestamps"] = False
        verdict = verify_bundle(bundle, self.ws.old, self.ws.new)
        self.assertFalse(verdict.ok)

    def test_verifier_binds_bundle_to_raw_inputs(self):
        _, result, bundle, _ = run_case(self.ws)
        # Verify the same bundle against a third unrelated tree must fail.
        other = Workspace()
        try:
            other.write_old("a.txt", b"something-else")
            other.write_new("a.txt", b"two")
            verdict = verify_bundle(bundle, other.old, other.new)
            self.assertFalse(verdict.ok)
        finally:
            other.cleanup()
