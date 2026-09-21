import copy
import json
import os
import random
import struct
import tempfile
import unittest
import warnings
import zipfile
from io import BytesIO
from pathlib import Path

from reprodiff import (
    Comparator,
    JsonKeyRule,
    Limits,
    RuleError,
    RuleSet,
    Verdict,
    ZipTimestampRule,
    compare_trees,
    load_evidence,
    save_evidence,
    verify_evidence,
)

DT1 = (2022, 2, 2, 2, 2, 2)
DT2 = (2023, 3, 3, 3, 3, 6)


def make_zip(entries, date_time=DT1):
    buf = BytesIO()
    with zipfile.ZipFile(buf, "w") as zf:
        for name, data in entries:
            info = zipfile.ZipInfo(name, date_time)
            info.compress_type = zipfile.ZIP_STORED
            info.external_attr = 0o644 << 16
            zf.writestr(info, data)
    return buf.getvalue()


def rebuild_zip(source, mutate=None, date_time=None):
    """Rewrite a zip preserving every entry, optionally changing content/time."""
    zin = zipfile.ZipFile(BytesIO(source))
    out = BytesIO()
    with zipfile.ZipFile(out, "w") as zout:
        for info in zin.infolist():
            data = zin.read(info)
            if mutate is not None:
                data = mutate(info.filename, data)
            clone = zipfile.ZipInfo(info.filename, date_time or info.date_time)
            clone.compress_type = info.compress_type
            clone.external_attr = info.external_attr
            clone.create_system = info.create_system
            zout.writestr(clone, data)
    return out.getvalue()


def write_file(root, rel, data, mode=None):
    path = Path(root) / rel
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)
    if mode is not None:
        os.chmod(path, mode)
    return path


def result_and_report(testcase, left, right, rules=None, limits=None):
    result = compare_trees(left, right, rules=rules, limits=limits)
    evidence = result.to_evidence()
    report = verify_evidence(left, right, rules or RuleSet(), evidence)
    testcase.assertTrue(
        report.ok, msg=f"verifier rejected own evidence: {report.problems}"
    )
    return result, evidence, report


class TreeCase(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        base = Path(self._tmp.name)
        self.left = base / "left"
        self.right = base / "right"
        self.left.mkdir()
        self.right.mkdir()

    def fresh_pair(self):
        base = Path(self._tmp.name)
        name = f"pair-{random.randrange(10**9)}"
        left = base / name / "left"
        right = base / name / "right"
        left.mkdir(parents=True)
        right.mkdir(parents=True)
        return left, right


class BasicComparisonTests(TreeCase):
    def test_identical_trees(self):
        write_file(self.left, "a/b.txt", b"same", mode=0o644)
        write_file(self.right, "a/b.txt", b"same", mode=0o644)
        result, evidence, _ = result_and_report(self, self.left, self.right)
        self.assertIs(result.verdict, Verdict.IDENTICAL)
        self.assertEqual(evidence["ignored"], [])

    def test_plain_content_difference(self):
        write_file(self.left, "f.bin", b"abcd")
        write_file(self.right, "f.bin", b"abXd")
        result, evidence, _ = result_and_report(self, self.left, self.right)
        self.assertIs(result.verdict, Verdict.DIFFERENT)
        self.assertEqual(result.differences[0].kind, "file-content-changed")

    def test_path_and_type_changes(self):
        write_file(self.left, "only_left", b"x")
        write_file(self.right, "only_right", b"x")
        write_file(self.left, "shape", b"data")
        os.symlink("target", self.right / "shape")
        result, _, _ = result_and_report(self, self.left, self.right)
        kinds = {diff.kind for diff in result.differences}
        self.assertIn("path-only-in-left", kinds)
        self.assertIn("path-only-in-right", kinds)
        self.assertIn("type-changed", kinds)

    def test_mode_change_is_substantive(self):
        write_file(self.left, "run.sh", b"echo", mode=0o755)
        write_file(self.right, "run.sh", b"echo", mode=0o644)
        result, _, _ = result_and_report(self, self.left, self.right)
        self.assertIs(result.verdict, Verdict.DIFFERENT)
        self.assertEqual(result.differences[0].kind, "mode-changed")

    def test_symlink_target_change(self):
        os.symlink("targets/a.txt", self.left / "link")
        os.symlink("targets/b.txt", self.right / "link")
        write_file(self.left, "targets/a.txt", b"x")
        write_file(self.right, "targets/a.txt", b"x")
        write_file(self.left, "targets/b.txt", b"x")
        write_file(self.right, "targets/b.txt", b"x")
        result, evidence, _ = result_and_report(self, self.left, self.right)
        self.assertIs(result.verdict, Verdict.DIFFERENT)
        diff = next(d for d in result.differences if d.kind == "symlink-target-changed")
        self.assertEqual(diff.detail, {"left": "targets/a.txt", "right": "targets/b.txt"})

    def test_unused_rule_recorded(self):
        rules = RuleSet([JsonKeyRule("j", "config.json", ("ts",))])
        write_file(self.left, "config.json", b'{"ts": 1, "k": 2}')
        write_file(self.right, "config.json", b'{"ts": 1, "k": 2}')
        result, _, _ = result_and_report(self, self.left, self.right, rules)
        self.assertIs(result.verdict, Verdict.IDENTICAL)
        self.assertEqual(result.unused_rules, ["j"])


class ZipTests(TreeCase):
    def test_zip_timestamp_only_difference_is_equivalent(self):
        left_zip = make_zip([("a.txt", b"content"), ("b.txt", b"other")])
        right_zip = rebuild_zip(left_zip, date_time=DT2)
        write_file(self.left, "pkg.zip", left_zip)
        write_file(self.right, "pkg.zip", right_zip)
        rules = RuleSet([ZipTimestampRule("zip-time", "pkg.zip")])
        result, evidence, report = result_and_report(
            self, self.left, self.right, rules
        )
        self.assertIs(result.verdict, Verdict.EQUIVALENT)
        self.assertEqual(len(evidence["ignored"]), 2)
        record = evidence["ignored"][0]
        self.assertEqual(record["rule_id"], "zip-time")
        self.assertTrue(record["field_path"].endswith("#date_time"))
        self.assertEqual(record["old"], "2022-02-02T02:02:02")
        self.assertEqual(record["new"], "2023-03-03T03:03:06")
        self.assertTrue(record["old_digest"].startswith("sha256:"))
        self.assertTrue(record["new_digest"].startswith("sha256:"))
        self.assertNotEqual(record["old_digest"], record["new_digest"])

    def test_timestamp_rule_without_rule_is_difference(self):
        left_zip = make_zip([("a.txt", b"content")])
        right_zip = rebuild_zip(left_zip, date_time=DT2)
        write_file(self.left, "pkg.zip", left_zip)
        write_file(self.right, "pkg.zip", right_zip)
        result, _, _ = result_and_report(self, self.left, self.right)
        self.assertIs(result.verdict, Verdict.DIFFERENT)
        self.assertEqual(result.differences[0].kind, "zip-entry-metadata-changed")

    def test_extended_timestamp_extra_not_ignored(self):
        # The parser-located DOS date_time is identical; the difference lives
        # inside the 0x5455 extended-timestamp extra field and stays substantive.
        def zip_with_unix_time(unix_time):
            info = zipfile.ZipInfo("a.txt", DT1)
            info.compress_type = zipfile.ZIP_STORED
            info.external_attr = 0o644 << 16
            info.extra = (
                struct.pack("<HHB I", 0x5455, 5, 0, unix_time)
            )
            buf = BytesIO()
            with zipfile.ZipFile(buf, "w") as zf:
                zf.writestr(info, b"content")
            return buf.getvalue()

        write_file(self.left, "pkg.zip", zip_with_unix_time(1_000_000))
        write_file(self.right, "pkg.zip", zip_with_unix_time(9_999_999))
        rules = RuleSet([ZipTimestampRule("zip-time", "pkg.zip")])
        result, _, _ = result_and_report(self, self.left, self.right, rules)
        self.assertIs(result.verdict, Verdict.DIFFERENT)
        diff = next(
            d for d in result.differences
            if d.kind == "zip-entry-metadata-changed"
        )
        self.assertEqual(diff.detail["field"], "extra")

    def test_payload_fake_timestamp_is_not_ignored(self):
        # Fake local-header bytes live INSIDE a normal payload (offset 8);
        # they must be compared as data, never treated as archive metadata.
        fake = (
            b"PAYLOAD:" + b"PK\x03\x04" + bytes(6)
            + b"\x00\x00\x21\x08" + bytes(20)
        )
        left_zip = make_zip([("payload.bin", fake + b"real bytes")])

        def flip_fake(name, data):
            mutable = bytearray(data)
            mutable[8 + 10] ^= 0xFF
            mutable[8 + 12] ^= 0x01
            return bytes(mutable)

        right_zip = rebuild_zip(left_zip, mutate=flip_fake)
        write_file(self.left, "pkg.zip", left_zip)
        write_file(self.right, "pkg.zip", right_zip)
        write_file(self.left, "fake-header.bin", fake)
        write_file(self.right, "fake-header.bin", bytes(flip_fake("fake-header.bin", fake)))
        rules = RuleSet([ZipTimestampRule("zip-time", "pkg.zip")])
        result, _, _ = result_and_report(self, self.left, self.right, rules)
        self.assertIs(result.verdict, Verdict.DIFFERENT)
        paths = {diff.path for diff in result.differences}
        self.assertIn("pkg.zip!payload.bin", paths)
        self.assertIn("fake-header.bin", paths)

    def test_duplicate_members_undetermined(self):
        buf = BytesIO()
        with warnings.catch_warnings():
            warnings.simplefilter("ignore", UserWarning)
            with zipfile.ZipFile(buf, "w") as zf:
                zf.writestr("dup.txt", b"first")
                zf.writestr("dup.txt", b"second")
                zf.writestr("other.txt", b"x")
        write_file(self.left, "bad.zip", make_zip([("dup.txt", b"first"), ("other.txt", b"x")]))
        write_file(self.right, "bad.zip", buf.getvalue())
        result, _, _ = result_and_report(self, self.left, self.right)
        self.assertIs(result.verdict, Verdict.UNDETERMINED)
        self.assertIn("duplicate entry name", result.undetermined[0].reason)

    def test_path_traversal_undetermined(self):
        write_file(self.left, "bad.zip", make_zip([("safe.txt", b"x")]))
        write_file(self.right, "bad.zip", make_zip([("../evil.txt", b"x")]))
        result, _, _ = result_and_report(self, self.left, self.right)
        self.assertIs(result.verdict, Verdict.UNDETERMINED)
        self.assertIn("unsafe entry path", result.undetermined[0].reason)

    def test_corrupt_archive_undetermined(self):
        valid = make_zip([("a.txt", b"alpha")])
        other = make_zip([("a.txt", b"beta!!")])
        write_file(self.left, "c.zip", valid)
        write_file(self.right, "c.zip", other[: len(other) // 2])
        result, _, _ = result_and_report(self, self.left, self.right)
        self.assertIs(result.verdict, Verdict.UNDETERMINED)
        self.assertIn("corrupt ZIP archive", result.undetermined[0].reason)

    def test_budgets_depth_bytes_entries(self):
        deep_left = make_zip([("inner.zip", make_zip([("inner2.zip",
            make_zip([("x.txt", b"a")]) )]))])
        deep_right = make_zip([("inner.zip", make_zip([("inner2.zip",
            make_zip([("x.txt", b"b")]) )]))])
        write_file(self.left, "d.zip", deep_left)
        write_file(self.right, "d.zip", deep_right)
        depth_result, _, _ = result_and_report(
            self, self.left, self.right, limits=Limits(max_depth=2)
        )
        self.assertIs(depth_result.verdict, Verdict.UNDETERMINED)
        self.assertIn("nesting depth", depth_result.undetermined[0].reason)

        big_left = make_zip([("big.bin", b"x" * 200)])
        big_right = make_zip([("big.bin", b"y" * 200)])
        left2, right2 = self.fresh_pair()
        write_file(left2, "b.zip", big_left)
        write_file(right2, "b.zip", big_right)
        byte_result, _, _ = result_and_report(
            self, left2, right2, limits=Limits(max_bytes=100)
        )
        self.assertIs(byte_result.verdict, Verdict.UNDETERMINED)
        self.assertIn("decompressed byte limit", byte_result.undetermined[0].reason)

        many_left = make_zip([("a", b"a"), ("b", b"b")])
        many_right = make_zip([("a", b"a"), ("b", b"c")])
        left3, right3 = self.fresh_pair()
        write_file(left3, "m.zip", many_left)
        write_file(right3, "m.zip", many_right)
        entry_result, _, _ = result_and_report(
            self, left3, right3, limits=Limits(max_entries=2)
        )
        self.assertIs(entry_result.verdict, Verdict.UNDETERMINED)
        self.assertIn("entry count limit", entry_result.undetermined[0].reason)


class NestedZipJsonTests(TreeCase):
    def test_nested_zip_json_big_integer(self):
        big_left = 2**80 + 12345678901234567890
        big_right = 2**80 + 98765432109876543210
        config_left = json.dumps(
            {"meta": {"build_id": big_left, "channel": "stable"},
             "items": [1, 2, 3]}
        ).encode()
        config_right = json.dumps(
            {"meta": {"build_id": big_right, "channel": "stable"},
             "items": [1, 2, 3]}
        ).encode()
        inner_left = make_zip([("config.json", config_left)])
        inner_right = make_zip([("config.json", config_right)])
        write_file(self.left, "app.zip", make_zip([("libs/inner.zip", inner_left)]))
        write_file(self.right, "app.zip", make_zip([("libs/inner.zip", inner_right)]))
        rules = RuleSet([
            JsonKeyRule(
                "ignore-build-id",
                "app.zip!libs/inner.zip!config.json",
                ("meta", "build_id"),
            )
        ])
        result, evidence, report = result_and_report(
            self, self.left, self.right, rules
        )
        self.assertIs(result.verdict, Verdict.EQUIVALENT)
        record = evidence["ignored"][0]
        self.assertEqual(record["rule_id"], "ignore-build-id")
        self.assertEqual(record["field_path"], "meta.build_id")
        self.assertEqual(record["old"], {"@type": "bigint", "value": str(big_left)})
        self.assertEqual(record["new"], {"@type": "bigint", "value": str(big_right)})
        self.assertNotEqual(record["old"], record["new"])

        evidence_path = Path(self._tmp.name) / "evidence.json"
        save_evidence(evidence, evidence_path)
        reloaded = load_evidence(evidence_path)
        second_report = verify_evidence(self.left, self.right, rules, reloaded)
        self.assertTrue(second_report.ok, second_report.problems)

    def test_protected_json_key_in_nested_zip_differs(self):
        inner_left = make_zip([("c.json", b'{"a": 1, "b": 2}')])
        inner_right = make_zip([("c.json", b'{"a": 9, "b": 2}')])
        write_file(self.left, "o.zip", make_zip([("n.zip", inner_left)]))
        write_file(self.right, "o.zip", make_zip([("n.zip", inner_right)]))
        rules = RuleSet([
            JsonKeyRule("r", "o.zip!n.zip!c.json", ("b",))
        ])
        result, _, _ = result_and_report(self, self.left, self.right, rules)
        self.assertIs(result.verdict, Verdict.DIFFERENT)
        self.assertEqual(result.differences[0].detail["key_path"], "a")


class JsonTests(TreeCase):
    def _json_pair(self, left_bytes, right_bytes, rules=None):
        write_file(self.left, "doc.json", left_bytes)
        write_file(self.right, "doc.json", right_bytes)
        return result_and_report(self, self.left, self.right, rules)

    def test_ignored_key_keeps_diff_evidence(self):
        rules = RuleSet([JsonKeyRule("ts", "doc.json", ("meta", "ts"))])
        result, evidence, _ = self._json_pair(
            b'{"meta": {"ts": "2020", "v": 1}, "n": 3}',
            b'{"meta": {"ts": "2030", "v": 1}, "n": 3}',
            rules,
        )
        self.assertIs(result.verdict, Verdict.EQUIVALENT)
        record = evidence["ignored"][0]
        self.assertEqual(record["old"], "2020")
        self.assertEqual(record["new"], "2030")
        self.assertEqual(record["kind"], "value-changed")
        self.assertTrue(record["old_digest"].startswith("sha256:"))

    def test_array_order_is_preserved(self):
        result, _, _ = self._json_pair(b'{"a": [1, 2, 3]}', b'{"a": [1, 3, 2]}')
        self.assertIs(result.verdict, Verdict.DIFFERENT)
        key_paths = {diff.detail["key_path"] for diff in result.differences}
        self.assertEqual(key_paths, {"a[1]", "a[2]"})

    def test_formatting_only_difference_is_substantive(self):
        result, _, _ = self._json_pair(
            b'{"a": 1, "b": 2}', b'{\n  "b": 2,\n  "a": 1\n}'
        )
        self.assertIs(result.verdict, Verdict.DIFFERENT)
        self.assertEqual(result.differences[0].kind, "json-formatting-changed")

    def test_duplicate_keys_rejected(self):
        result, _, _ = self._json_pair(
            b'{"a": 1, "a": 2}', b'{"a": 1, "a": 3}',
            RuleSet([JsonKeyRule("r", "doc.json", ("a",))]),
        )
        self.assertIs(result.verdict, Verdict.UNDETERMINED)
        self.assertIn("duplicate object key", result.undetermined[0].reason)

    def test_integer_precision_not_lost(self):
        result, _, _ = self._json_pair(
            b'{"n": 9007199254740993}',
            b'{"n": 9007199254740992}',
        )
        self.assertIs(result.verdict, Verdict.DIFFERENT)
        self.assertEqual(result.differences[0].kind, "value-changed")
        result2, _, _ = self._json_pair(
            b'{"n": 1.0000000000000001}',
            b'{"n": 1.0000000000000002}',
        )
        self.assertIs(result2.verdict, Verdict.DIFFERENT)

    def test_big_decimal_in_ignored_evidence(self):
        rules = RuleSet([JsonKeyRule("r", "doc.json", ("x",))])
        result, evidence, _ = self._json_pair(
            b'{"x": 1.0000000000000001}',
            b'{"x": 1.0000000000000002}',
            rules,
        )
        self.assertIs(result.verdict, Verdict.EQUIVALENT)
        self.assertEqual(
            evidence["ignored"][0]["old"],
            {"@type": "decimal", "value": "1.0000000000000001"},
        )

    def test_key_added_removed_through_rule(self):
        rules = RuleSet([JsonKeyRule("r", "doc.json", ("build",))])
        result, evidence, _ = self._json_pair(
            b'{"keep": 1, "build": {"t": 2}}',
            b'{"keep": 1}',
            rules,
        )
        self.assertIs(result.verdict, Verdict.EQUIVALENT)
        kinds = {record["kind"] for record in evidence["ignored"]}
        self.assertEqual(kinds, {"key-removed"})


class RuleValidationTests(TreeCase):
    def test_overlapping_json_rules(self):
        with self.assertRaises(RuleError):
            RuleSet([
                JsonKeyRule("r1", "doc.json", ("a",)),
                JsonKeyRule("r2", "doc.json", ("a", "b")),
            ])
        with self.assertRaises(RuleError):
            JsonKeyRule("r1", "doc.json", ())

    def test_duplicate_ids_and_rule_types(self):
        with self.assertRaises(RuleError):
            RuleSet([
                ZipTimestampRule("same", "x.zip"),
                ZipTimestampRule("same", "y.zip"),
            ])
        with self.assertRaises(RuleError):
            RuleSet([
                JsonKeyRule("mix", "p", ("k",)),
                ZipTimestampRule("mix", "p"),
            ])

    def test_target_type_mismatch(self):
        write_file(self.left, "doc.zip", make_zip([("a", b"a")]))
        write_file(self.right, "doc.zip", make_zip([("a", b"b")]))
        with self.assertRaises(RuleError):
            compare_trees(
                self.left, self.right,
                RuleSet([JsonKeyRule("bad", "doc.zip", ("k",))]),
            )
        write_file(self.left, "text.txt", b"aaaa")
        write_file(self.right, "text.txt", b"aaab")
        with self.assertRaises(RuleError):
            compare_trees(
                self.left, self.right,
                RuleSet([ZipTimestampRule("bad", "text.txt")]),
            )
        os.makedirs(self.left / "sub")
        os.makedirs(self.right / "sub")
        with self.assertRaises(RuleError):
            compare_trees(
                self.left, self.right,
                RuleSet([JsonKeyRule("bad", "sub", ("k",))]),
            )


class FuzzAndEvidenceTests(TreeCase):
    CONFIG_PATH = "config.json"
    ZIP_PATH = "pkg.zip"

    def _rules(self):
        return RuleSet([
            JsonKeyRule("json-ts", self.CONFIG_PATH, ("ts",)),
            ZipTimestampRule("zip-ts", self.ZIP_PATH),
        ])

    def _build(self, left, right, ts_value, zip_dt,
               blob_flip=None, payload_flip=None,
               keep="alpha", number=5):
        rng = random.Random(7)
        blob = bytes(rng.getrandbits(8) for _ in range(256))
        payload = bytes(rng.getrandbits(8) for _ in range(512))

        def mutate_blob(data, flip):
            if flip is None:
                return data
            data = bytearray(data)
            data[flip] ^= 0xFF
            return bytes(data)

        config = (
            json.dumps({"ts": "2026-01-01T00:00:00Z", "keep": "alpha", "n": 5})
            .encode()
        )
        right_config = json.dumps(
            {"ts": ts_value, "keep": keep, "n": number}
        ).encode()
        base_zip = make_zip(
            [("payload.bin", payload), ("doc.txt", b"hello world")],
            date_time=DT1,
        )

        def mutate(name, data):
            if name == "payload.bin" and payload_flip is not None:
                data = bytearray(data)
                data[payload_flip] ^= 0xFF
                data = bytes(data)
            return data

        right_zip = rebuild_zip(base_zip, mutate=mutate, date_time=zip_dt)
        write_file(left, self.CONFIG_PATH, config)
        write_file(right, self.CONFIG_PATH, right_config)
        write_file(left, "blob.bin", blob)
        write_file(right, "blob.bin", mutate_blob(blob, blob_flip))
        write_file(left, self.ZIP_PATH, base_zip)
        write_file(right, self.ZIP_PATH, right_zip)

    def test_baseline_equivalent_with_evidence(self):
        self._build(self.left, self.right, "2027-12-31T23:59:58Z", DT2)
        result, evidence, report = result_and_report(
            self, self.left, self.right, self._rules()
        )
        self.assertIs(result.verdict, Verdict.EQUIVALENT)
        ids = {record["rule_id"] for record in evidence["ignored"]}
        self.assertEqual(ids, {"json-ts", "zip-ts"})
        self.assertTrue(report.ok)

    def test_random_protected_byte_never_ignored(self):
        rng = random.Random(20260921)
        for iteration in range(30):
            left, right = self.fresh_pair()
            choice = iteration % 4
            blob_flip = rng.randrange(256) if choice == 0 else None
            payload_flip = rng.randrange(512) if choice == 1 else None
            keep = f"alpha-{iteration}" if choice == 2 else "alpha"
            number = 100 + iteration if choice == 3 else 5
            self._build(
                left, right, "2027-12-31T23:59:58Z", DT2,
                blob_flip=blob_flip, payload_flip=payload_flip,
                keep=keep, number=number,
            )
            result, evidence, report = result_and_report(
                self, left, right, self._rules()
            )
            self.assertIs(
                result.verdict, Verdict.DIFFERENT,
                msg=f"protected change accepted at iteration {iteration}",
            )
            self.assertTrue(report.ok)

    def test_random_ignorable_field_keeps_equivalence(self):
        rng = random.Random(987654321)
        for iteration in range(10):
            left, right = self.fresh_pair()
            ts_value = f"2026-02-{10 + iteration:02d}T{rng.randrange(24):02d}:00:00Z"
            zip_dt = (
                1990 + rng.randrange(30),
                1 + rng.randrange(12),
                1 + rng.randrange(28),
                rng.randrange(24),
                rng.randrange(60),
                2 * rng.randrange(30),
            )
            self._build(left, right, ts_value, zip_dt)
            result, evidence, report = result_and_report(
                self, left, right, self._rules()
            )
            self.assertIs(result.verdict, Verdict.EQUIVALENT)
            self.assertTrue(evidence["ignored"])
            self.assertTrue(report.ok)

    def test_tampered_evidence_rejected(self):
        self._build(self.left, self.right, "2027-12-31T23:59:58Z", DT2)
        rules = self._rules()
        result = Comparator(rules).compare_trees(self.left, self.right)
        original = result.to_evidence()

        tampered_verdict = copy.deepcopy(original)
        tampered_verdict["verdict"] = "identical"
        report = verify_evidence(self.left, self.right, rules, tampered_verdict)
        self.assertFalse(report.ok)

        tampered_value = copy.deepcopy(original)
        json_records = [r for r in tampered_value["ignored"]
                        if r["rule_id"] == "json-ts"]
        json_records[0]["new"] = "1900-01-01T00:00:00Z"
        report = verify_evidence(self.left, self.right, rules, tampered_value)
        self.assertFalse(report.ok)

        missing_record = copy.deepcopy(original)
        missing_record["ignored"].pop()
        report = verify_evidence(self.left, self.right, rules, missing_record)
        self.assertFalse(report.ok)

        tampered_input = copy.deepcopy(original)
        tampered_input["inputs"]["left"] = "sha256:" + "0" * 64
        report = verify_evidence(self.left, self.right, rules, tampered_input)
        self.assertFalse(report.ok)

        swapped_inputs = copy.deepcopy(original)
        swapped_inputs["inputs"]["left"], swapped_inputs["inputs"]["right"] = (
            swapped_inputs["inputs"]["right"], swapped_inputs["inputs"]["left"],
        )
        report = verify_evidence(self.left, self.right, rules, swapped_inputs)
        self.assertFalse(report.ok)


if __name__ == "__main__":
    unittest.main(verbosity=2)
