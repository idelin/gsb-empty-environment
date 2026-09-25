import unittest

from evidence.manifest import build_manifest, iter_blocks
from evidence.receiver import (Chunk, ChunkConflictError, DUPLICATE,
                               REJECTED_STALE, STORED, Receiver,
                               StaleManifestError)
from evidence.sample import build_sample_manifest, feed_out_of_order
from evidence.store import Store


class ReceiverTest(unittest.TestCase):
    def setUp(self):
        self.store = Store(":memory:")
        self.receiver = Receiver(self.store)

    def test_out_of_order_then_duplicate_is_idempotent(self):
        manifest, files = build_sample_manifest(chunk_size=64)
        self.receiver.receive_manifest(manifest)
        statuses = feed_out_of_order(self.receiver, manifest, files, seed=7)
        self.assertIn(STORED, statuses)

        chunks = [Chunk(manifest.manifest_id, manifest.version,
                        entry.file_id, index, block)
                  for entry in manifest.files
                  for index, block in enumerate(
                      iter_blocks(files[entry.file_id], entry.chunk_size))]
        self.assertTrue(chunks)
        for chunk in chunks:
            self.assertEqual(self.receiver.receive_chunk(chunk), DUPLICATE)

        rows = self.store.conn.execute("SELECT COUNT(*) AS n FROM chunks").fetchone()
        self.assertEqual(rows["n"], len(chunks))

    def test_same_id_different_digest_conflicts(self):
        manifest, files = build_sample_manifest(chunk_size=64)
        self.receiver.receive_manifest(manifest)
        feed_out_of_order(self.receiver, manifest, files, seed=7)
        target = manifest.files[0]
        good = next(iter_blocks(files[target.file_id], target.chunk_size))
        altered = bytes(b ^ 0xFF for b in good[:8]) + good[8:]
        bad = Chunk(manifest.manifest_id, manifest.version,
                    target.file_id, 0, altered)
        with self.assertRaises(ChunkConflictError):
            self.receiver.receive_chunk(bad)
        # The bad bytes were never persisted.
        row = self.store.conn.execute(
            "SELECT digest FROM chunks WHERE file_id=? AND idx=0",
            (target.file_id,)).fetchone()
        self.assertEqual(row["digest"], target.chunk_digests[0])

    def test_late_tail_chunk_of_old_manifest_is_not_merged(self):
        # v1 package: feed every chunk except the last one of report.md.
        v1, files_v1 = build_sample_manifest(version=1, chunk_size=64)
        self.assertEqual(self.receiver.receive_manifest(v1), "stored-new")
        held_back = None
        for entry in v1.files:
            for index, block in enumerate(
                    iter_blocks(files_v1[entry.file_id], entry.chunk_size)):
                chunk = Chunk(v1.manifest_id, 1, entry.file_id, index, block)
                if entry.file_id == "report.md" and \
                        index == entry.chunk_count - 1:
                    held_back = chunk
                else:
                    self.receiver.receive_chunk(chunk)
        self.assertIsNotNone(held_back)

        # Corrected v2 manifest arrives before the stale tail chunk.
        v2, files_v2 = build_sample_manifest(version=2, chunk_size=64)
        self.assertEqual(self.receiver.receive_manifest(v2), "updated")
        feed_out_of_order(self.receiver, v2, files_v2, seed=99)

        # The v1 tail chunk arrives late: rejected, never merged into v2.
        self.assertEqual(self.receiver.receive_chunk(held_back),
                         REJECTED_STALE)
        v2_row = self.store.conn.execute(
            "SELECT COUNT(*) AS n FROM chunks WHERE manifest_version=2").fetchone()
        expected = sum(entry.chunk_count for entry in v2.files)
        self.assertEqual(v2_row["n"], expected)
        v1_row = self.store.conn.execute(
            "SELECT COUNT(*) AS n FROM chunks WHERE manifest_version=1").fetchone()
        self.assertEqual(v1_row["n"], expected - 1)

        with self.assertRaises(StaleManifestError):
            self.receiver.receive_manifest(v1)


if __name__ == "__main__":
    unittest.main()
