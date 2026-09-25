"""Export a verified package to a directory, streaming chunks to disk so a
large file never needs to live in memory."""
from __future__ import annotations

import json
import os

from evidence.manifest import Manifest
from evidence.receiver import load_current_manifest
from evidence.store import Store


def export_package(store: Store, manifest_id: str, dest: str) -> str:
    manifest = load_current_manifest(store, manifest_id)
    os.makedirs(os.path.join(dest, "files"), exist_ok=True)

    for entry in manifest.files:
        target = os.path.join(dest, "files", entry.file_id)
        with open(target, "wb") as handle:
            for index in range(entry.chunk_count):
                row = store.conn.execute(
                    "SELECT data FROM chunks WHERE manifest_id=? "
                    "AND manifest_version=? AND file_id=? AND idx=?",
                    (manifest_id, manifest.version, entry.file_id, index),
                ).fetchone()
                if row is None:
                    raise FileNotFoundError(
                        f"missing chunk {entry.file_id}[{index}]")
                handle.write(row["data"])

    signoff_row = store.conn.execute(
        "SELECT id, decision, status, created_at FROM signoffs "
        "WHERE manifest_id=? AND manifest_version=? ORDER BY id DESC LIMIT 1",
        (manifest_id, manifest.version),
    ).fetchone()
    signoff_body = None
    if signoff_row is not None:
        signoff_body = {
            "signoff_id": signoff_row["id"],
            "decision": signoff_row["decision"],
            "status": signoff_row["status"],
            "created_at": signoff_row["created_at"],
            "manifest_id": manifest_id,
            "manifest_version": manifest.version,
            "manifest_digest": manifest.digest,
        }

    manifest_body = manifest.to_dict()
    manifest_body["digest"] = manifest.digest
    with open(os.path.join(dest, "manifest.json"), "w", encoding="utf-8") as fh:
        json.dump(manifest_body, fh, sort_keys=True, indent=2)
    with open(os.path.join(dest, "signoff.json"), "w", encoding="utf-8") as fh:
        json.dump(signoff_body, fh, sort_keys=True, indent=2)
    return dest
