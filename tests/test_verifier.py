import json
import os
import shutil
import tempfile
import unittest

from evidence.exporter import export_package
from evidence.manifest import iter_blocks
from evidence.receiver import Chunk, Receiver
from evidence.sample import build_sample_manifest, feed_out_of_order
from evidence.signoff import sign_off
from evidence.store import Store
from evidence.verifier import verify_package


def _exported_case(version=1):
    store = Store(":memory:")
    receiver = Receiver(store)
    manifest, files = build_sample_manifest(version=version, chunk_size=64)
    receiver.receive_manifest(manifest)
    feed_out_of_order(receiver, manifest, files)
    result = sign_off(store, manifest.manifest_id)
    assert result.ok, result.errors
    dest = tempfile.mkdtemp(prefix="evidence-export-")
    export_package(store, manifest.manifest_id, dest)
    return dest


class VerifierTest(unittest.TestCase):
    def setUp(self):
        self.dest = _exported_case()

    def tearDown(self):
        shutil.rmtree(self.dest, ignore_errors=True)

    def test_clean_package_verifies_and_replays(self):
        report = verify_package(self.dest)
        self.assertTrue(report.ok, [f.code for f in report.failures])

    def test_tampered_content_is_located(self):
        path = os.path.join(self.dest, "files", "readings.csv")
        with open(path, "rb") as fh:
            data = fh.read()
        with open(path, "wb") as fh:
            fh.write(bytes([data[0] ^ 0x01]) + data[1:])
        report = verify_package(self.dest)
        self.assertFalse(report.ok)
        self.assertIn("CONTENT_MISMATCH", report.codes())
        mismatch = next(f for f in report.failures if f.code == "CONTENT_MISMATCH")
        self.assertIn("readings.csv", mismatch.location)

    def test_tampered_chunk_order_is_located(self):
        path = os.path.join(self.dest, "files", "derivation-log")
        with open(path, "rb") as fh:
            data = fh.read()
        with open(os.path.join(self.dest, "manifest.json"), encoding="utf-8") as fh:
            manifest = json.load(fh)
        entry = next(e for e in manifest["files"] if e["file_id"] == "derivation-log")
        size = entry["chunk_size"]
        block_a = data[0:size]
        block_b = data[size:2 * size]
        self.assertEqual(len(block_a), len(block_b))
        swapped = block_b + block_a + data[2 * size:]
        with open(path, "wb") as fh:
            fh.write(swapped)
        report = verify_package(self.dest)
        self.assertFalse(report.ok)
        self.assertIn("ORDER_MISMATCH", report.codes())
        failure = next(f for f in report.failures if f.code == "ORDER_MISMATCH")
        self.assertIn("derivation-log", failure.location)
        self.assertIn("index 1", failure.detail)

    def test_tampered_manifest_is_located(self):
        path = os.path.join(self.dest, "manifest.json")
        with open(path, encoding="utf-8") as fh:
            manifest = json.load(fh)
        manifest["files"][0]["file_digest"] = "0" * 64
        with open(path, "w", encoding="utf-8") as fh:
            json.dump(manifest, fh, sort_keys=True, indent=2)
        report = verify_package(self.dest)
        self.assertFalse(report.ok)
        self.assertIn("MANIFEST_TAMPERED", report.codes())
        failure = next(f for f in report.failures
                       if f.code == "MANIFEST_TAMPERED")
        self.assertEqual(failure.location, "manifest.json")

    def test_tampered_replay_is_located(self):
        path = os.path.join(self.dest, "files", "derivation-log")
        with open(path, "rb") as fh:
            data = fh.read()
        marker = b'"fact_id":"fact-mean","value":'
        pos = data.index(marker) + len(marker)
        digit = data[pos:pos + 1]
        replacement = b"8" if digit != b"8" else b"9"
        tampered = data[:pos] + replacement + data[pos + 1:]
        with open(path, "wb") as fh:
            fh.write(tampered)
        report = verify_package(self.dest)
        self.assertFalse(report.ok)
        self.assertIn("REPLAY_MISMATCH", report.codes())
        failure = next(f for f in report.failures
                       if f.code == "REPLAY_MISMATCH")
        self.assertIn("step-mean", failure.location)


if __name__ == "__main__":
    unittest.main()
