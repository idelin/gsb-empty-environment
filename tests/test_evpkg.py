"""End-to-end tests for the evidence package pipeline (stdlib unittest)."""

from __future__ import annotations

import json
import random
import shutil
import tempfile
import tracemalloc
import unittest
from pathlib import Path

from evpkg import (
    Receiver,
    Store,
    build_sample_package,
    compute_replay_digest,
    export_package,
    verify_package,
)
from evpkg.errors import ConflictError, LateChunkError, SignoffBlocked
from evpkg.hashing import sha256_hex
from evpkg.model import facts_from_bytes, replay, rules_from_bytes, steps_from_bytes
from evpkg.verifier import (
    ChunkOrderError,
    ContentTamperedError,
    ManifestTamperedError,
    MissingReferenceError,
    ReplayMismatchError,
)

CHUNK = 65536


class EvpkgTestCase(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp(prefix="evpkg-"))
        self.addCleanup(shutil.rmtree, self.tmp, True)
        self.db = self.tmp / "evidence.db"
        self.store = Store(self.db)
        self.receiver = Receiver(self.store, replay_fn=compute_replay_digest)

    # -- helpers -------------------------------------------------------------

    def feed(self, pkg, receiver=None, shuffle=True):
        """Feed every chunk (out of order by default); return per-chunk results."""
        receiver = receiver or self.receiver
        items = [
            (fid, idx, data)
            for fid, parts in pkg.chunks.items()
            for idx, data in enumerate(parts)
        ]
        if shuffle:
            random.Random(2024).shuffle(items)
        results = []
        for fid, idx, data in items:
            results.append(
                receiver.receive_chunk(
                    pkg.manifest.package_id,
                    pkg.manifest.version,
                    fid,
                    idx,
                    sha256_hex(data),
                    data,
                )
            )
        return results

    def export(self, pkg):
        dest = self.tmp / f"export-v{pkg.manifest.version}"
        return export_package(self.store, pkg.manifest.package_id, pkg.manifest.version, dest)

    def counts(self, table):
        return self.store.query(f"SELECT COUNT(*) AS c FROM {table}")[0]["c"]

    # -- reception ------------------------------------------------------------

    def test_out_of_order_receive_and_idempotent_duplicate(self):
        pkg = build_sample_package(telemetry_bytes=200_000, chunk_size=CHUNK)
        self.assertEqual(self.receiver.register_manifest(pkg.manifest), "registered")
        first = self.feed(pkg)
        self.assertTrue(all(r == "stored" for r in first))
        second = self.feed(pkg)
        self.assertTrue(all(r == "duplicate" for r in second))
        total = sum(len(p) for p in pkg.chunks.values())
        self.assertEqual(self.counts("chunks"), total)
        report = self.receiver.validate(pkg.manifest.package_id, 1)
        self.assertTrue(report.ok, report.reasons())

    def test_conflicting_chunk_digest_rejected(self):
        pkg = build_sample_package()
        self.receiver.register_manifest(pkg.manifest)
        self.feed(pkg)
        data = b"tampered replacement for facts.json chunk 0"
        with self.assertRaises(ConflictError):
            self.receiver.receive_chunk(
                pkg.manifest.package_id, 1, "facts.json", 0, sha256_hex(data), data
            )

    def test_same_package_id_different_digest_conflicts(self):
        pkg1 = build_sample_package(version=1)
        pkg2 = build_sample_package(version=1, telemetry_bytes=4096)
        self.assertEqual(pkg1.manifest.package_id, pkg2.manifest.package_id)
        self.assertNotEqual(pkg1.manifest.digest, pkg2.manifest.digest)
        self.assertEqual(self.receiver.register_manifest(pkg1.manifest), "registered")
        with self.assertRaises(ConflictError):
            self.receiver.register_manifest(pkg2.manifest)
        self.assertEqual(self.receiver.register_manifest(pkg1.manifest), "duplicate")

    # -- cross-file references --------------------------------------------------

    def test_all_files_present_but_missing_reference_blocks_signoff(self):
        pkg = build_sample_package(
            reference_overrides={"steps.json": ("facts.json", "rules.json", "ghost.json")}
        )
        self.receiver.register_manifest(pkg.manifest)
        self.feed(pkg)
        report = self.receiver.validate(pkg.manifest.package_id, 1)
        self.assertFalse(report.ok)
        self.assertIn(("steps.json", "ghost.json"), report.missing_references)
        self.assertEqual(report.missing_files, [])
        with self.assertRaises(SignoffBlocked):
            self.receiver.publish(pkg.manifest.package_id, 1)
        self.assertEqual(self.counts("signoffs"), 0)
        self.assertEqual(self.counts("outbox"), 0)

    # -- manifest versioning ------------------------------------------------------

    def test_late_chunk_from_old_manifest_rejected(self):
        v1 = build_sample_package(version=1, telemetry_bytes=200_000, chunk_size=CHUNK)
        self.receiver.register_manifest(v1.manifest)
        last_idx = len(v1.chunks["telemetry.bin"]) - 1
        for fid, parts in v1.chunks.items():
            for idx, data in enumerate(parts):
                if fid == "telemetry.bin" and idx == last_idx:
                    continue  # the tail chunk arrives late
                self.receiver.receive_chunk(
                    v1.manifest.package_id, 1, fid, idx, sha256_hex(data), data
                )
        v2 = build_sample_package(version=2, temperature=65.5, telemetry_bytes=200_000)
        self.assertEqual(self.receiver.register_manifest(v2.manifest), "registered")
        self.feed(v2)
        tail = v1.chunks["telemetry.bin"][last_idx]
        with self.assertRaises(LateChunkError):
            self.receiver.receive_chunk(
                v1.manifest.package_id, 1, "telemetry.bin", last_idx, sha256_hex(tail), tail
            )
        stored_v1 = self.store.query(
            "SELECT COUNT(*) AS c FROM chunks WHERE manifest_version=1"
        )[0]["c"]
        self.assertEqual(stored_v1, sum(len(p) for p in v1.chunks.values()) - 1)
        report = self.receiver.validate(v2.manifest.package_id, 2)
        self.assertTrue(report.ok, report.reasons())
        self.receiver.publish(v2.manifest.package_id, 2)

    def test_correction_after_signoff_invalidates_and_requires_reverification(self):
        v1 = build_sample_package(version=1)
        self.receiver.register_manifest(v1.manifest)
        self.feed(v1)
        self.receiver.publish(v1.manifest.package_id, 1)
        self.assertEqual(self.receiver.signoff_rows()[0]["status"], "valid")

        v2 = build_sample_package(version=2, temperature=65.5)
        self.receiver.register_manifest(v2.manifest)

        signoffs = self.receiver.signoff_rows()
        self.assertEqual(signoffs[0]["status"], "invalidated")
        outbox = self.receiver.outbox_rows()
        kinds = [r["kind"] for r in outbox]
        self.assertIn("signoff_invalidated", kinds)
        manifest_v2 = self.store.query(
            "SELECT status FROM manifests WHERE version=2"
        )[0]["status"]
        self.assertEqual(manifest_v2, "pending_reverification")

        self.feed(v2)
        self.receiver.publish(v2.manifest.package_id, 2)
        statuses = {r["manifest_version"]: r["status"] for r in self.receiver.signoff_rows()}
        self.assertEqual(statuses, {1: "invalidated", 2: "valid"})
        self.assertEqual(self.receiver.check_invariants(), [])

    # -- crash / recovery ---------------------------------------------------------

    def test_crash_after_validation_before_publish_recovers_cleanly(self):
        pkg = build_sample_package(version=1)
        self.receiver.register_manifest(pkg.manifest)
        self.feed(pkg)
        report = self.receiver.validate(pkg.manifest.package_id, 1)
        self.assertTrue(report.ok, report.reasons())
        # simulated crash: process vanishes between validation and publish
        store2 = Store(self.db)
        receiver2 = Receiver(store2, replay_fn=compute_replay_digest)
        self.assertEqual(self.counts("signoffs"), 0)
        self.assertEqual(self.counts("outbox"), 0)
        self.assertEqual(receiver2.check_invariants(), [])
        signoff_id = receiver2.publish(pkg.manifest.package_id, 1)
        self.assertGreater(signoff_id, 0)
        self.assertEqual(receiver2.check_invariants(), [])

    def test_crash_mid_commit_rolls_back_signoff_and_notification(self):
        pkg = build_sample_package(version=1)
        self.receiver.register_manifest(pkg.manifest)
        self.feed(pkg)

        def boom(conn):
            raise RuntimeError("simulated crash after outbox insert")

        with self.assertRaises(RuntimeError):
            self.receiver.publish(pkg.manifest.package_id, 1, before_commit=boom)
        # recovery: sign-off and notification share one persistence decision
        store2 = Store(self.db)
        receiver2 = Receiver(store2, replay_fn=compute_replay_digest)
        self.assertEqual(self.counts("signoffs"), 0)
        self.assertEqual(self.counts("outbox"), 0)
        self.assertEqual(receiver2.check_invariants(), [])
        receiver2.publish(pkg.manifest.package_id, 1)
        self.assertEqual(self.counts("signoffs"), 1)
        self.assertEqual(self.counts("outbox"), 1)

    # -- outbox -----------------------------------------------------------------

    def test_duplicate_delivery_confirmation_is_idempotent(self):
        pkg = build_sample_package(version=1)
        self.receiver.register_manifest(pkg.manifest)
        self.feed(pkg)
        self.receiver.publish(pkg.manifest.package_id, 1)
        notification = self.receiver.outbox_rows()[0]
        self.assertEqual(notification["kind"], "package_signed_off")
        self.assertTrue(self.receiver.confirm_delivery(notification["id"]))
        self.assertFalse(self.receiver.confirm_delivery(notification["id"]))
        delivered = self.store.query(
            "SELECT COUNT(*) AS c FROM outbox WHERE delivered=1"
        )[0]["c"]
        self.assertEqual(delivered, 1)

    # -- memory bound ---------------------------------------------------------

    def test_chunked_receive_and_validate_bounds_memory(self):
        size = 4 * 1024 * 1024
        pkg = build_sample_package(telemetry_bytes=size, chunk_size=CHUNK)
        self.receiver.register_manifest(pkg.manifest)
        tracemalloc.start()
        try:
            self.feed(pkg)
            report = self.receiver.validate(pkg.manifest.package_id, 1)
            _, peak = tracemalloc.get_traced_memory()
        finally:
            tracemalloc.stop()
        self.assertTrue(report.ok, report.reasons())
        self.assertLess(
            peak,
            2 * 1024 * 1024,
            f"peak python memory {peak} bytes should stay far below file size {size}",
        )

    # -- replay -----------------------------------------------------------------

    def test_replay_is_deterministic_and_versioned(self):
        pkg = build_sample_package()
        file_bytes = {fid: b"".join(parts) for fid, parts in pkg.chunks.items()}
        first = compute_replay_digest(file_bytes)
        second = compute_replay_digest(file_bytes)
        self.assertEqual(first, second)
        self.assertEqual(first, pkg.manifest.replay_digest)
        results = replay(
            facts_from_bytes(file_bytes["facts.json"]),
            rules_from_bytes(file_bytes["rules.json"]),
            steps_from_bytes(file_bytes["steps.json"]),
        )
        self.assertEqual(results[0]["step_id"], "S-001")
        self.assertEqual(results[0]["step_version"], 1)
        self.assertEqual(
            [r["rule_id"] for r in results[0]["rules"]], ["R-001", "R-002"]
        )

    # -- independent export verifier --------------------------------------------

    def test_verifier_accepts_untouched_export(self):
        pkg = build_sample_package(telemetry_bytes=200_000, chunk_size=CHUNK)
        self.receiver.register_manifest(pkg.manifest)
        self.feed(pkg)
        dest = self.export(pkg)
        manifest = verify_package(dest, replay_fn=compute_replay_digest)
        self.assertEqual(manifest.digest, pkg.manifest.digest)

    def test_verifier_detects_tampered_content(self):
        pkg = build_sample_package(telemetry_bytes=200_000, chunk_size=CHUNK)
        self.receiver.register_manifest(pkg.manifest)
        self.feed(pkg)
        dest = self.export(pkg)
        victim = dest / "chunks" / "telemetry.bin" / "000003.chunk"
        data = bytearray(victim.read_bytes())
        data[0] ^= 0xFF
        victim.write_bytes(bytes(data))
        with self.assertRaises(ContentTamperedError) as ctx:
            verify_package(dest, replay_fn=compute_replay_digest)
        self.assertIn("telemetry.bin", str(ctx.exception))

    def test_verifier_detects_reordered_chunks(self):
        pkg = build_sample_package(telemetry_bytes=200_000, chunk_size=CHUNK)
        self.receiver.register_manifest(pkg.manifest)
        self.feed(pkg)
        dest = self.export(pkg)
        cdir = dest / "chunks" / "telemetry.bin"
        first, second = cdir / "000000.chunk", cdir / "000001.chunk"
        swap = cdir / "swap.tmp"
        first.rename(swap)
        second.rename(first)
        swap.rename(second)
        with self.assertRaises(ChunkOrderError) as ctx:
            verify_package(dest, replay_fn=compute_replay_digest)
        self.assertIn("chunk 0", str(ctx.exception))

    def test_verifier_detects_tampered_manifest(self):
        pkg = build_sample_package(telemetry_bytes=200_000, chunk_size=CHUNK)
        self.receiver.register_manifest(pkg.manifest)
        self.feed(pkg)
        dest = self.export(pkg)
        mpath = dest / "manifest.json"
        doc = json.loads(mpath.read_text())
        doc["files"][0]["sha256"] = "0" * 64
        mpath.write_text(json.dumps(doc, indent=2, sort_keys=True))
        with self.assertRaises(ManifestTamperedError):
            verify_package(dest, replay_fn=compute_replay_digest)

    def test_verifier_detects_missing_reference(self):
        pkg = build_sample_package(
            reference_overrides={"steps.json": ("facts.json", "ghost.json")}
        )
        self.receiver.register_manifest(pkg.manifest)
        self.feed(pkg)
        dest = self.export(pkg)
        with self.assertRaises(MissingReferenceError) as ctx:
            verify_package(dest, replay_fn=compute_replay_digest)
        self.assertIn("ghost.json", str(ctx.exception))

    def test_verifier_detects_replay_mismatch(self):
        pkg = build_sample_package(replay_digest_override="0" * 64)
        self.receiver.register_manifest(pkg.manifest)
        self.feed(pkg)
        dest = self.export(pkg)
        with self.assertRaises(ReplayMismatchError):
            verify_package(dest, replay_fn=compute_replay_digest)


if __name__ == "__main__":
    unittest.main()
