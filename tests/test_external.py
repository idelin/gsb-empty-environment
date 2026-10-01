"""Tests for the resumable external-memory shuffle with crash recovery."""

import json
import os
import shutil
import tempfile
import unittest

from unbiased_shuffle import (
    BlockedRecordStore,
    CorruptionError,
    ExternalShuffler,
    SimulatedCrash,
    SplitMix64Source,
    initialize_record_file,
)

RECORD_SIZE = 8
N = 40
SEED = 424242


def make_records(n=N):
    return [i.to_bytes(RECORD_SIZE, "big") for i in range(n)]


class FlakyFault:
    """Fault hook: fail the first `fail_per_attempt` attempts of every op."""

    def __init__(self, fail_attempts=2):
        self.fail_attempts = fail_attempts
        self.injected = 0

    def __call__(self, op, attempt):
        if attempt < self.fail_attempts:
            self.injected += 1
            raise OSError("simulated transient I/O error")


class CrashAfterSteps:
    """Crash hook: raise SimulatedCrash at `phase` of swap number steps+1.

    `steps` counts fully checkpointed swaps.  Phases before_draw /
    after_draw / after_journal / after_swap of swap k fire when
    steps == k-1; after_checkpoint fires when steps == k.
    """

    def __init__(self, steps, phase):
        self.steps = steps
        self.phase = phase
        self.count = 0

    def __call__(self, phase, frontier):
        if phase == "after_checkpoint":
            self.count += 1
        if self.count == self.steps and phase == self.phase:
            raise SimulatedCrash(f"crash at {phase} after {self.count} swaps")


class ExternalShuffleCase(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp(prefix="extshuf_")
        self.addCleanup(shutil.rmtree, self.dir, ignore_errors=True)
        self.data_path = os.path.join(self.dir, "data.bin")
        self.meta_dir = os.path.join(self.dir, "meta")
        os.makedirs(self.meta_dir, exist_ok=True)
        initialize_record_file(self.data_path, RECORD_SIZE, make_records())

    def _new_shuffler(self, block_size=1, fault=None, crash_hook=None, seed=SEED):
        store = BlockedRecordStore(
            self.data_path, RECORD_SIZE, N, block_size=block_size, fault=fault
        )
        rng = SplitMix64Source(seed)
        shuffler = ExternalShuffler(store, rng, seed, self.meta_dir, crash_hook)
        return shuffler, store

    def _run_continuous(self, block_size=1, fault=None, seed=SEED):
        shuffler, store = self._new_shuffler(block_size, fault)
        try:
            shuffler.run()
            return store.read_all(), list(shuffler.trace), shuffler.samples
        finally:
            store.close()

    def _reset_data(self):
        initialize_record_file(self.data_path, RECORD_SIZE, make_records())
        for name in os.listdir(self.meta_dir):
            os.remove(os.path.join(self.meta_dir, name))


class ContinuousRunTest(ExternalShuffleCase):
    def test_result_is_a_permutation_and_deterministic(self):
        records, trace, samples = self._run_continuous()
        self.assertEqual(sorted(records), make_records())  # no dup / no loss
        self.assertNotEqual(records, make_records())
        self.assertEqual(samples, N - 1)  # exactly one sample per swap
        frontiers = [t[0] for t in trace]
        self.assertEqual(frontiers, list(range(N - 1, 0, -1)))
        for i, j in trace:
            self.assertTrue(0 <= j <= i)
        again, _, _ = self._run_continuous()  # second run on shuffled data
        self.assertEqual(sorted(again), make_records())

    def test_checkpoint_contents(self):
        self._run_continuous()
        with open(os.path.join(self.meta_dir, "checkpoint.json")) as fh:
            cp = json.load(fh)
        self.assertEqual(cp["seed"], SEED)
        self.assertEqual(cp["frontier"], 0)
        self.assertEqual(cp["published_from"], 1)
        self.assertEqual(cp["samples"], N - 1)
        self.assertIn("rng_state", cp)
        self.assertFalse(os.path.exists(os.path.join(self.meta_dir, "journal.json")))


class BlockSizeAndRetryTest(ExternalShuffleCase):
    """Random integer sampling must not depend on block size or I/O retries."""

    def test_block_size_does_not_change_result(self):
        reference, ref_trace, ref_samples = self._run_continuous(block_size=1)
        for block_size in (2, 3, 5, 8, 64):
            self._reset_data()
            records, trace, samples = self._run_continuous(block_size=block_size)
            self.assertEqual(records, reference)
            self.assertEqual(trace, ref_trace)
            self.assertEqual(samples, ref_samples)

    def test_io_retries_do_not_change_result(self):
        reference, ref_trace, _ = self._run_continuous()
        self._reset_data()
        fault = FlakyFault(fail_attempts=2)  # every low-level op fails twice
        shuffler, store = self._new_shuffler(block_size=3, fault=fault)
        try:
            shuffler.run()
            self.assertGreater(store.retries, 0)  # retries actually happened
            self.assertEqual(store.read_all(), reference)
            self.assertEqual(shuffler.trace, ref_trace)
        finally:
            store.close()


class CrashRecoveryTest(ExternalShuffleCase):
    """Crash at every phase; recovery must equal the continuous run exactly."""

    MID_PHASES = ("before_draw", "after_draw", "after_journal", "after_swap")

    def test_crash_and_recover_everywhere(self):
        reference, ref_trace, ref_samples = self._run_continuous()
        cases = [
            (steps, phase)
            for steps in (0, 1, 2, 5, 13, N - 2)
            for phase in self.MID_PHASES
        ]
        cases += [(steps, "after_checkpoint") for steps in (1, 2, 5, 13, N - 1)]
        for steps, phase in cases:
            with self.subTest(steps=steps, phase=phase):
                self._reset_data()
                hook = CrashAfterSteps(steps, phase)
                shuffler, store = self._new_shuffler(block_size=3, crash_hook=hook)
                try:
                    with self.assertRaises(SimulatedCrash):
                        shuffler.run()
                finally:
                    store.close()  # lose in-memory buffers, like a real crash
                # Recover with a fresh store and a fresh rng (same seed).
                recovered, store2 = self._new_shuffler(block_size=3)
                try:
                    recovered.run()
                    self.assertEqual(store2.read_all(), reference)
                    self.assertEqual(recovered.trace, ref_trace)
                    self.assertEqual(recovered.samples, ref_samples)
                finally:
                    store2.close()

    def test_recovery_does_not_repeat_swaps(self):
        # The recovered run reproduces the reference trace exactly: the
        # pending swap is not re-drawn and no swap is applied twice.
        _, ref_trace, ref_samples = self._run_continuous()
        self._reset_data()
        hook = CrashAfterSteps(7, "after_swap")
        shuffler, store = self._new_shuffler(crash_hook=hook)
        try:
            with self.assertRaises(SimulatedCrash):
                shuffler.run()
            crashed_samples = shuffler.samples
            crashed_trace = list(shuffler.trace)
        finally:
            store.close()
        recovered, store2 = self._new_shuffler()
        try:
            recovered.run()
            # 7 swaps checkpointed + 1 drawn (pending in journal) at crash time.
            self.assertEqual(crashed_samples, 8)
            self.assertEqual(crashed_trace, ref_trace[:7])
            # Recovered state is cumulative and equals the clean run exactly.
            self.assertEqual(recovered.samples, ref_samples)
            self.assertEqual(recovered.trace, ref_trace)
        finally:
            store2.close()

    def test_seed_mismatch_is_rejected(self):
        hook = CrashAfterSteps(3, "after_checkpoint")
        shuffler, store = self._new_shuffler(crash_hook=hook)
        try:
            with self.assertRaises(SimulatedCrash):
                shuffler.run()
        finally:
            store.close()
        recovered, store2 = self._new_shuffler(seed=SEED + 1)
        try:
            with self.assertRaises(CorruptionError):
                recovered.run()
        finally:
            store2.close()


if __name__ == "__main__":
    unittest.main()
