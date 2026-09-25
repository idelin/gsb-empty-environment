"""Reception of manifests and out-of-order chunks."""
from __future__ import annotations

import json
import time
from dataclasses import dataclass

from evidence.manifest import Manifest, sha256_hex
from evidence.store import Store


class ChunkConflictError(Exception):
    """Same content identity carries a different digest than one already seen."""


class UnknownManifestError(Exception):
    pass


class StaleManifestError(Exception):
    """A chunk for a superseded manifest version arrived too late."""


class ManifestConflictError(Exception):
    """Same manifest id+version received with a different overall digest."""


@dataclass(frozen=True)
class Chunk:
    manifest_id: str
    manifest_version: int
    file_id: str
    index: int
    data: bytes

    @property
    def digest(self) -> str:
        return sha256_hex(self.data)


STORED = "stored"
DUPLICATE = "duplicate"
REJECTED_STALE = "rejected-stale"


def _current_version(store: Store, manifest_id: str) -> int | None:
    row = store.conn.execute(
        "SELECT value FROM meta WHERE key=?",
        (f"current_version:{manifest_id}",),
    ).fetchone()
    return None if row is None else int(row["value"])


def _load_manifest(store: Store, manifest_id: str, version: int) -> Manifest:
    row = store.conn.execute(
        "SELECT body FROM manifests WHERE manifest_id=? AND version=?",
        (manifest_id, version),
    ).fetchone()
    return Manifest.from_dict(json.loads(row["body"]))


def load_current_manifest(store: Store, manifest_id: str) -> Manifest:
    version = _current_version(store, manifest_id)
    if version is None:
        raise UnknownManifestError(manifest_id)
    return _load_manifest(store, manifest_id, version)


class Receiver:
    def __init__(self, store: Store):
        self.store = store

    def receive_manifest(self, manifest: Manifest) -> str:
        """Persist a manifest version.

        Returns ``stored-new`` for the first version, ``duplicate`` when the
        identical version arrives again, or ``updated`` when a newer version
        supersedes the current one (old sign-offs are invalidated atomically).
        """
        store = self.store
        body = json.dumps(manifest.to_dict(), sort_keys=True,
                          separators=(",", ":"))
        current = _current_version(store, manifest.manifest_id)

        if current is not None and manifest.version < current:
            raise StaleManifestError(
                f"manifest v{manifest.version} older than current v{current}")

        if current is not None:
            existing = store.conn.execute(
                "SELECT digest FROM manifests WHERE manifest_id=? AND version=?",
                (manifest.manifest_id, manifest.version),
            ).fetchone()
            if existing is not None:
                if existing["digest"] != manifest.digest:
                    raise ManifestConflictError(
                        f"{manifest.manifest_id}@v{manifest.version} "
                        "received with two different digests")
                return DUPLICATE
        with store.atomic():
            status = "current" if current is None else "pending_reverification"
            store.conn.execute(
                "INSERT INTO manifests (manifest_id, version, body, digest,"
                " status, received_at) VALUES (?,?,?,?,?,?)",
                (manifest.manifest_id, manifest.version, body, manifest.digest,
                 status, time.time()),
            )
            if current is not None:
                store.conn.execute(
                    "UPDATE manifests SET status='superseded' "
                    "WHERE manifest_id=? AND version=?",
                    (manifest.manifest_id, current),
                )
                # Old sign-offs become invalid; notice shares the same decision.
                for row in store.conn.execute(
                    "SELECT id FROM signoffs WHERE manifest_id=? "
                    "AND manifest_version=? AND status='valid'",
                    (manifest.manifest_id, current),
                ).fetchall():
                    store.conn.execute(
                        "UPDATE signoffs SET status='superseded' WHERE id=?",
                        (row["id"],),
                    )
                store.conn.execute(
                    "INSERT INTO outbox (signoff_id, kind, payload, created_at)"
                    " VALUES (NULL, 'reverification_required', ?, ?)",
                    (json.dumps({"manifest_id": manifest.manifest_id,
                                 "old_version": current,
                                 "new_version": manifest.version}),
                     time.time()),
                )
            store.conn.execute(
                "INSERT INTO meta(key, value) VALUES(?, ?) "
                "ON CONFLICT(key) DO UPDATE SET value=excluded.value",
                (f"current_version:{manifest.manifest_id}",
                 str(manifest.version)),
            )
        return "stored-new" if current is None else "updated"

    def receive_chunk(self, chunk: Chunk) -> str:
        store = self.store
        current = _current_version(store, chunk.manifest_id)
        if current is None:
            raise UnknownManifestError(chunk.manifest_id)
        if chunk.manifest_version < current:
            # Late tail chunk of an old manifest: never merge into the new one.
            return REJECTED_STALE
        if chunk.manifest_version > current:
            raise UnknownManifestError(
                f"chunk for unknown manifest v{chunk.manifest_version}")

        manifest = _load_manifest(store, chunk.manifest_id, current)
        entry = manifest.file_entry(chunk.file_id)
        if entry is None:
            raise ChunkConflictError(
                f"unknown file_id {chunk.file_id} for "
                f"{chunk.manifest_id}@v{current}")
        if not 0 <= chunk.index < entry.chunk_count:
            raise ChunkConflictError(
                f"chunk index {chunk.index} out of range for {chunk.file_id}")

        actual = chunk.digest
        declared = entry.chunk_digests[chunk.index]
        if actual != declared:
            raise ChunkConflictError(
                f"digest conflict for {chunk.file_id}[{chunk.index}]: "
                f"declared={declared} received={actual}")

        row = store.conn.execute(
            "SELECT digest FROM chunks WHERE manifest_id=? AND "
            "manifest_version=? AND file_id=? AND idx=?",
            (chunk.manifest_id, current, chunk.file_id, chunk.index),
        ).fetchone()
        if row is not None:
            if row["digest"] != actual:
                raise ChunkConflictError(
                    f"chunk {chunk.file_id}[{chunk.index}] already stored "
                    "with a different digest")
            return DUPLICATE

        store.conn.execute(
            "INSERT INTO chunks (manifest_id, manifest_version, file_id,"
            " idx, digest, data) VALUES (?,?,?,?,?,?)",
            (chunk.manifest_id, current, chunk.file_id, chunk.index,
             actual, chunk.data),
        )
        return STORED
