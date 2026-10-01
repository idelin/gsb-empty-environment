"""Crash-recovery tests: real subprocess restarts with fault injection."""

import json
import os
import random
import subprocess
import sys
import tempfile
import unittest

from common import oracle_versions, random_script

from sr_engine import CorruptionError, DuplicateTxnError, Engine, get_semiring

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
WORKER = os.path.join(ROOT, "tests", "crash_worker.py")

ALL_FAULT_POINTS = [
    "log:before_write",
    "log:after_write_before_fsync",
    "log:after_fsync",
    "log:partial_write",
    "ckpt:before_tmp_write",
    "ckpt:after_tmp_write",
    "ckpt:after_rename",
    "ckpt:before_log_rotate",
    "ckpt:after_log_rotate",
]


def run_worker(path, spec, fault_at=None, fault_skip=0, expect_crash=False):
    os.makedirs(path, exist_ok=True)
    script = os.path.join(path, "script.json")
    with open(script, "w") as f:
        json.dump(spec, f)
    env = dict(os.environ)
    env.pop("SR_FAULT_AT", None)
    env.pop("SR_FAULT_SKIP", None)
    if fault_at:
        env["SR_FAULT_AT"] = fault_at
        env["SR_FAULT_SKIP"] = str(fault_skip)
    proc = subprocess.run(
        [sys.executable, WORKER, path, script],
        capture_output=True, text=True, env=env,
    )
    if expect_crash:
        assert proc.returncode != 0, f"expected crash, got success: {proc.stdout}"
    else:
        assert proc.returncode == 0, f"worker failed: {proc.stderr}"
    return proc


def make_spec(sr_name, m, k, n, txns, checkpoint_after=()):
    return {
        "m": m, "k": k, "n": n, "semiring": sr_name,
        "txns": txns, "checkpoint_after": list(checkpoint_after),
    }


def assert_versions_match_oracle(testcase, path, sr_name, m, k, n, txns):
    """Every retained version must show inputs and outputs in agreement."""
    sr = get_semiring(sr_name)
    expected = oracle_versions(sr, m, k, n, txns)
    eng = Engine.open(path)
    testcase.assertEqual(eng.version, len(txns))
    retained = eng.retained_versions()
    testcase.assertIn(len(txns), retained)
    for version in retained:
        A, B, C = expected[version]
        with eng.snapshot(version) as snap:
            testcase.assertEqual(dict(snap.A), A, f"version {version} A")
            testcase.assertEqual(dict(snap.B), B, f"version {version} B")
            testcase.assertEqual(dict(snap.C), C, f"version {version} C")
    eng.close()


class RecoveryBase(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.dir = os.path.join(self.tmp.name, "db")
        rng = random.Random(99)
        self.m, self.k, self.n = 4, 5, 4
        self.sr_name = "minplus"
        self.txns = random_script(
            rng, get_semiring(self.sr_name), self.m, self.k, self.n, steps=8)


class TestCrashAtEveryBoundary(RecoveryBase):
    def test_crash_and_recover_at_all_persistence_boundaries(self):
        for point in ALL_FAULT_POINTS:
            with self.subTest(point=point):
                self.dir = os.path.join(self.tmp.name, f"db-{point}")
                spec = make_spec(self.sr_name, self.m, self.k, self.n,
                                 self.txns, checkpoint_after=[3])
                run_worker(self.dir, spec, fault_at=point, expect_crash=True)
                # Restart with no faults: recovery + full script rerun must
                # succeed (idempotent txn ids make re-committing safe).
                run_worker(self.dir, spec)
                assert_versions_match_oracle(
                    self, self.dir, self.sr_name, self.m, self.k, self.n,
                    self.txns)

    def test_crash_at_second_checkpoint_via_skip(self):
        # Skip the create-time checkpoint fault; crash at the mid-run one.
        spec = make_spec(self.sr_name, self.m, self.k, self.n,
                         self.txns, checkpoint_after=[5])
        run_worker(self.dir, spec, fault_at="ckpt:after_tmp_write",
                   fault_skip=1, expect_crash=True)
        run_worker(self.dir, spec)
        assert_versions_match_oracle(
            self, self.dir, self.sr_name, self.m, self.k, self.n, self.txns)


class TestLogCorruption(RecoveryBase):
    def test_truncated_tail_ignored(self):
        spec = make_spec(self.sr_name, self.m, self.k, self.n, self.txns)
        run_worker(self.dir, spec, fault_at="log:partial_write", expect_crash=True)
        run_worker(self.dir, spec)  # recovers, truncates tail, completes
        assert_versions_match_oracle(
            self, self.dir, self.sr_name, self.m, self.k, self.n, self.txns)

    def test_mid_log_corruption_is_fatal(self):
        spec = make_spec(self.sr_name, self.m, self.k, self.n, self.txns)
        run_worker(self.dir, spec)
        log = os.path.join(self.dir, "wal.log")
        with open(log, "rb") as f:
            data = bytearray(f.read())
        first_nl = data.index(b"\n")
        data[first_nl // 2] ^= 0x01  # flip a bit inside the first record
        with open(log, "wb") as f:
            f.write(data)
        with self.assertRaises(CorruptionError):
            Engine.open(self.dir)

    def test_checkpoint_corruption_is_fatal(self):
        spec = make_spec(self.sr_name, self.m, self.k, self.n, self.txns)
        run_worker(self.dir, spec)
        ckpt = os.path.join(self.dir, "checkpoint.json")
        with open(ckpt, "rb") as f:
            data = bytearray(f.read())
        data[len(data) // 2] ^= 0x01
        with open(ckpt, "wb") as f:
            f.write(data)
        with self.assertRaises(CorruptionError):
            Engine.open(self.dir)


class TestIdempotentRecovery(RecoveryBase):
    def test_retry_after_crash_returns_existing_commit(self):
        spec = make_spec(self.sr_name, self.m, self.k, self.n, self.txns)
        # Crash after the first txn record is durable but before publish.
        run_worker(self.dir, spec, fault_at="log:after_fsync", expect_crash=True)
        eng = Engine.open(self.dir)
        self.assertEqual(eng.version, 1)  # durable txn was recovered
        first = self.txns[0]
        # Same txn id + same content: returns the existing commit.
        self.assertEqual(eng.commit(first["txn_id"], first["ops"]), 1)
        self.assertEqual(eng.version, 1)
        # Same txn id + different content: rejected.
        with self.assertRaises(DuplicateTxnError):
            eng.commit(first["txn_id"], [["set", "A", 0, 0, 123]])
        eng.close()

    def test_no_version_shows_inputs_without_outputs(self):
        # Crash between log fsync and in-memory publish, then recover:
        # the recovered version must already contain the maintained C.
        spec = make_spec("int", self.m, self.k, self.n, self.txns)
        run_worker(self.dir, spec, fault_at="log:after_fsync", expect_crash=True)
        run_worker(self.dir, spec)
        assert_versions_match_oracle(
            self, self.dir, "int", self.m, self.k, self.n, self.txns)


class TestCheckpointBoundaries(RecoveryBase):
    def test_crash_during_log_rotation(self):
        spec = make_spec(self.sr_name, self.m, self.k, self.n,
                         self.txns, checkpoint_after=[4])
        run_worker(self.dir, spec, fault_at="ckpt:before_log_rotate",
                   fault_skip=1, expect_crash=True)
        # Checkpoint renamed, log not yet truncated: replay must skip
        # records already covered by the checkpoint (no double apply).
        run_worker(self.dir, spec)
        assert_versions_match_oracle(
            self, self.dir, self.sr_name, self.m, self.k, self.n, self.txns)

    def test_checkpoint_then_more_txns_then_clean_restart(self):
        spec = make_spec(self.sr_name, self.m, self.k, self.n,
                         self.txns, checkpoint_after=[2, 5])
        run_worker(self.dir, spec)
        run_worker(self.dir, spec)  # clean reopen, idempotent replay
        assert_versions_match_oracle(
            self, self.dir, self.sr_name, self.m, self.k, self.n, self.txns)


if __name__ == "__main__":
    unittest.main()
