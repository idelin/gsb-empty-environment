"""Chunk reception, validation and atomic sign-off."""

from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass, field
from datetime import datetime, timezone

from .errors import (
    ChecksumError,
    ConflictError,
    EvpkgError,
    LateChunkError,
    SignoffBlocked,
    UnknownManifestError,
    UnknownNotificationError,
)
from .hashing import sha256_hex
from .manifest import Manifest
from .model import DOMAIN_FILES


def _utcnow() -> str:
    return datetime.now(timezone.utc).isoformat()


@dataclass
class ValidationReport:
    missing_files: list = field(default_factory=list)
    missing_references: list = field(default_factory=list)
    digest_mismatches: list = field(default_factory=list)
    replay_ok: bool = True

    @property
    def ok(self) -> bool:
        return (
            not self.missing_files
            and not self.missing_references
            and not self.digest_mismatches
            and self.replay_ok
        )

    def reasons(self) -> list:
        out = [f"file incomplete or missing: {f}" for f in self.missing_files]
        out += [f"unresolved reference: {src} -> {tgt}" for src, tgt in self.missing_references]
        out += [f"digest mismatch: {f}" for f in self.digest_mismatches]
        if not self.replay_ok:
            out.append("replay digest mismatch")
        return out


class Receiver:
    """Receives chunks out of order, validates, and signs off atomically."""

    def __init__(self, store, replay_fn=None):
        self.store = store
        self.replay_fn = replay_fn

    def register_manifest(self, manifest: Manifest) -> str:
        """Register a manifest version.

        Idempotent for an identical re-send. A new version supersedes the
        old one, invalidates any valid sign-off and records a
        re-verification notification in the same transaction.
        """
        now = _utcnow()
        with self.store.transaction() as conn:
            row = conn.execute(
                "SELECT digest FROM manifests WHERE package_id=? AND version=?",
                (manifest.package_id, manifest.version),
            ).fetchone()
            if row is not None:
                if row["digest"] != manifest.digest:
                    raise ConflictError(
                        f"manifest {manifest.package_id} v{manifest.version} already "
                        "registered with a different digest"
                    )
                return "duplicate"
            maxv = conn.execute(
                "SELECT COALESCE(MAX(version), 0) AS m FROM manifests WHERE package_id=?",
                (manifest.package_id,),
            ).fetchone()["m"]
            if manifest.version <= maxv:
                raise ConflictError(
                    f"manifest version must increase (have v{maxv}, got v{manifest.version})"
                )
            conn.execute(
                "UPDATE manifests SET status='superseded' WHERE package_id=?",
                (manifest.package_id,),
            )
            valid = conn.execute(
                "SELECT id, manifest_version FROM signoffs WHERE package_id=? AND status='valid'",
                (manifest.package_id,),
            ).fetchall()
            conn.execute(
                "UPDATE signoffs SET status='invalidated' WHERE package_id=? AND status='valid'",
                (manifest.package_id,),
            )
            for signoff in valid:
                conn.execute(
                    "UPDATE outbox SET retracted=1 WHERE signoff_id=? AND kind='package_signed_off'",
                    (signoff["id"],),
                )
                conn.execute(
                    "INSERT INTO outbox(signoff_id, kind, payload, created_at) VALUES(?,?,?,?)",
                    (
                        signoff["id"],
                        "signoff_invalidated",
                        json.dumps(
                            {
                                "package_id": manifest.package_id,
                                "invalidated_version": signoff["manifest_version"],
                                "pending_reverification_version": manifest.version,
                            },
                            sort_keys=True,
                        ),
                        now,
                    ),
                )
            conn.execute(
                "INSERT INTO manifests(package_id, version, digest, json, status, created_at)"
                " VALUES(?,?,?,?,?,?)",
                (
                    manifest.package_id,
                    manifest.version,
                    manifest.digest,
                    manifest.to_json(),
                    "pending_reverification",
                    now,
                ),
            )
        return "registered"

    def receive_chunk(self, package_id, manifest_version, file_id, chunk_index, digest, data) -> str:
        actual = sha256_hex(data)
        if actual != digest:
            raise ChecksumError(f"declared digest {digest} != actual {actual}")
        with self.store.transaction() as conn:
            row = conn.execute(
                "SELECT status, json FROM manifests WHERE package_id=? AND version=?",
                (package_id, manifest_version),
            ).fetchone()
            if row is None:
                raise UnknownManifestError(f"{package_id} v{manifest_version}")
            if row["status"] == "superseded":
                raise LateChunkError(
                    f"chunk for superseded manifest {package_id} v{manifest_version} rejected"
                )
            existing = conn.execute(
                "SELECT digest FROM chunks WHERE manifest_version=? AND file_id=? AND chunk_index=?",
                (manifest_version, file_id, chunk_index),
            ).fetchone()
            if existing is not None:
                if existing["digest"] == digest:
                    return "duplicate"
                raise ConflictError(
                    f"chunk {file_id}#{chunk_index} already stored with a different digest"
                )
            manifest = Manifest.from_json(row["json"])
            entry = manifest.file_entry(file_id)
            if not 0 <= chunk_index < entry.chunk_count:
                raise ChecksumError(f"chunk index {chunk_index} out of range for {file_id}")
            if entry.chunk_digests[chunk_index] != digest:
                raise ChecksumError(f"chunk {file_id}#{chunk_index} does not match the manifest")
            conn.execute(
                "INSERT INTO chunks(package_id, manifest_version, file_id, chunk_index, digest, data)"
                " VALUES(?,?,?,?,?,?)",
                (package_id, manifest_version, file_id, chunk_index, digest, data),
            )
        return "stored"

    def validate(self, package_id, version) -> ValidationReport:
        report = ValidationReport()
        conn = self.store.connect()
        try:
            row = conn.execute(
                "SELECT json FROM manifests WHERE package_id=? AND version=?",
                (package_id, version),
            ).fetchone()
            if row is None:
                raise UnknownManifestError(f"{package_id} v{version}")
            manifest = Manifest.from_json(row["json"])
            domain_bytes = {}
            for entry in manifest.files:
                cur = conn.execute(
                    "SELECT chunk_index, data FROM chunks"
                    " WHERE manifest_version=? AND file_id=? ORDER BY chunk_index",
                    (version, entry.file_id),
                )
                hasher = hashlib.sha256()
                count = 0
                consistent = True
                domain_parts = [] if entry.file_id in DOMAIN_FILES else None
                for chunk in cur:  # streamed row by row: memory stays bounded
                    if chunk["chunk_index"] != count:
                        consistent = False
                        break
                    data = chunk["data"]
                    if sha256_hex(data) != entry.chunk_digests[count]:
                        consistent = False
                        break
                    hasher.update(data)
                    if domain_parts is not None:
                        domain_parts.append(data)
                    count += 1
                if count != entry.chunk_count:
                    report.missing_files.append(entry.file_id)
                elif not consistent or hasher.hexdigest() != entry.sha256:
                    report.digest_mismatches.append(entry.file_id)
                elif domain_parts is not None:
                    domain_bytes[entry.file_id] = b"".join(domain_parts)
            known = {e.file_id for e in manifest.files}
            for entry in manifest.files:
                for ref in entry.references:
                    if ref not in known:
                        report.missing_references.append((entry.file_id, ref))
            if self.replay_fn is not None and not report.missing_files and not report.digest_mismatches:
                try:
                    report.replay_ok = self.replay_fn(domain_bytes) == manifest.replay_digest
                except Exception:
                    report.replay_ok = False
        finally:
            conn.close()
        return report

    def publish(self, package_id, version, before_commit=None) -> int:
        """Validate, then persist sign-off + outbox notification atomically.

        ``before_commit`` is a test hook invoked inside the transaction to
        simulate a crash between the writes and the commit.
        """
        report = self.validate(package_id, version)
        if not report.ok:
            raise SignoffBlocked(report.reasons())
        now = _utcnow()
        with self.store.transaction() as conn:
            row = conn.execute(
                "SELECT status, json FROM manifests WHERE package_id=? AND version=?",
                (package_id, version),
            ).fetchone()
            if row is None:
                raise UnknownManifestError(f"{package_id} v{version}")
            if row["status"] == "superseded":
                raise EvpkgError(f"cannot sign off superseded manifest {package_id} v{version}")
            manifest = Manifest.from_json(row["json"])
            cur = conn.execute(
                "INSERT INTO signoffs(package_id, manifest_version, manifest_digest, status, created_at)"
                " VALUES(?,?,?,'valid',?)",
                (package_id, version, manifest.digest, now),
            )
            signoff_id = cur.lastrowid
            conn.execute(
                "INSERT INTO outbox(signoff_id, kind, payload, created_at) VALUES(?,?,?,?)",
                (
                    signoff_id,
                    "package_signed_off",
                    json.dumps(
                        {
                            "package_id": package_id,
                            "version": version,
                            "manifest_digest": manifest.digest,
                        },
                        sort_keys=True,
                    ),
                    now,
                ),
            )
            conn.execute(
                "UPDATE manifests SET status='verified' WHERE package_id=? AND version=?",
                (package_id, version),
            )
            if before_commit is not None:
                before_commit(conn)
        return signoff_id

    def outbox_rows(self):
        return self.store.query("SELECT * FROM outbox ORDER BY id")

    def signoff_rows(self):
        return self.store.query("SELECT * FROM signoffs ORDER BY id")

    def confirm_delivery(self, notification_id) -> bool:
        """Idempotent delivery confirmation; second call is a no-op."""
        with self.store.transaction() as conn:
            row = conn.execute(
                "SELECT delivered FROM outbox WHERE id=?", (notification_id,)
            ).fetchone()
            if row is None:
                raise UnknownNotificationError(str(notification_id))
            if row["delivered"]:
                return False
            conn.execute("UPDATE outbox SET delivered=1 WHERE id=?", (notification_id,))
        return True

    def check_invariants(self) -> list:
        """No live 'signed off' notification may exist without a valid sign-off."""
        rows = self.store.query(
            "SELECT o.id AS nid FROM outbox o LEFT JOIN signoffs s ON o.signoff_id = s.id"
            " WHERE o.kind='package_signed_off' AND o.retracted=0"
            " AND (s.id IS NULL OR s.status != 'valid')"
        )
        return [f"outbox notification {r['nid']} has no valid sign-off" for r in rows]
