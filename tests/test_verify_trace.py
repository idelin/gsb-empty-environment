"""The verifier must catch contract violations, not just pass good traces."""
import json
import tempfile
import unittest
from pathlib import Path

from tools.verify_trace import verify_trace


def write_trace(directory, records):
    path = Path(directory) / "trace.jsonl"
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(json.dumps({"type": "open", "modulus": 256}) + "\n")
        for record in records:
            fh.write(json.dumps(record) + "\n")
    return str(path)


def deliver(partition, epoch, seq, **kw):
    record = {
        "type": "deliver",
        "partition": partition,
        "epoch": epoch,
        "seq": seq,
        "delivery_id": f"{partition}#{epoch}:0:{seq}",
        "payload_hash": "abc",
        "redelivery": False,
        "correction": False,
    }
    record.update(kw)
    return record


class VerifyTraceTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()

    def tearDown(self):
        self.tmp.cleanup()

    def test_clean_trace_passes(self):
        path = write_trace(self.tmp.name, [
            deliver("P", 0, 1),
            {"type": "skip", "proof_id": "P-sk0", "partition": "P",
             "epoch": 0, "gap_start": 2, "gap_end": 2},
            deliver("P", 0, 3),
            {"type": "ack", "delivery_id": "P#0:0:1"},
        ])
        self.assertEqual(verify_trace(path), [])

    def test_gap_crossing_without_proof_is_caught(self):
        path = write_trace(self.tmp.name, [
            deliver("P", 0, 1),
            deliver("P", 0, 3),  # skips 2 with no proof
        ])
        violations = verify_trace(path)
        self.assertTrue(any("without a skip proof" in v for v in violations))

    def test_double_delivery_without_redelivery_flag_is_caught(self):
        path = write_trace(self.tmp.name, [
            deliver("P", 0, 1),
            deliver("P", 0, 1),
        ])
        violations = verify_trace(path)
        self.assertTrue(any("delivered twice" in v for v in violations))

    def test_revoke_after_cross_is_caught(self):
        path = write_trace(self.tmp.name, [
            deliver("P", 0, 1),
            {"type": "skip", "proof_id": "P-sk0", "partition": "P",
             "epoch": 0, "gap_start": 2, "gap_end": 2},
            deliver("P", 0, 3),
            {"type": "revoke", "partition": "P", "proof_id": "P-sk0"},
        ])
        violations = verify_trace(path)
        self.assertTrue(any("revoked after" in v for v in violations))

    def test_ack_of_undelivered_is_caught(self):
        path = write_trace(self.tmp.name, [
            {"type": "ack", "delivery_id": "P#0:0:0:9"},
        ])
        violations = verify_trace(path)
        self.assertTrue(any("undelivered" in v for v in violations))

    def test_redelivery_with_same_payload_is_legal(self):
        path = write_trace(self.tmp.name, [
            deliver("P", 0, 1),
            deliver("P", 0, 1, redelivery=True),
        ])
        self.assertEqual(verify_trace(path), [])


if __name__ == "__main__":
    unittest.main()
