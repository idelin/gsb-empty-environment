"""Export a stored package to a portable directory layout."""

from __future__ import annotations

import shutil
from pathlib import Path

from .errors import EvpkgError, UnknownManifestError
from .manifest import Manifest


def export_package(store, package_id, version, dest) -> Path:
    """Write ``manifest.json`` plus one chunk file per stored chunk.

    Chunk files are named by zero-padded index so an independent verifier
    can detect reordering, loss and tampering without the database.
    """
    dest = Path(dest)
    conn = store.connect()
    try:
        row = conn.execute(
            "SELECT json FROM manifests WHERE package_id=? AND version=?",
            (package_id, version),
        ).fetchone()
        if row is None:
            raise UnknownManifestError(f"{package_id} v{version}")
        manifest = Manifest.from_json(row["json"])
        if dest.exists():
            shutil.rmtree(dest)
        dest.mkdir(parents=True)
        (dest / "manifest.json").write_text(manifest.to_json(), encoding="utf-8")
        for entry in manifest.files:
            cdir = dest / "chunks" / entry.file_id
            cdir.mkdir(parents=True)
            cur = conn.execute(
                "SELECT chunk_index, data FROM chunks"
                " WHERE manifest_version=? AND file_id=? ORDER BY chunk_index",
                (version, entry.file_id),
            )
            count = 0
            for chunk in cur:  # streamed: bounded memory even for big files
                (cdir / f"{chunk['chunk_index']:06d}.chunk").write_bytes(chunk["data"])
                count += 1
            if count != entry.chunk_count:
                raise EvpkgError(
                    f"cannot export {entry.file_id}: {count}/{entry.chunk_count} chunks stored"
                )
    finally:
        conn.close()
    return dest
