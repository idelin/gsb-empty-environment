"""Independent export verifier.

Reads only the exported directory (no database) and pinpoints the exact
failure cause: tampered manifest, tampered content, reordered chunks,
missing cross-file references or replay mismatch.
"""

from __future__ import annotations

import hashlib
import json
from pathlib import Path

from .hashing import sha256_hex
from .manifest import Manifest
from .model import DOMAIN_FILES


class VerificationError(Exception):
    """Base class for verification failures."""


class ManifestTamperedError(VerificationError):
    """manifest.json does not match its own overall digest."""


class ContentTamperedError(VerificationError):
    """Chunk bytes do not match any digest declared in the manifest."""


class ChunkOrderError(VerificationError):
    """Chunks are missing, extra, or contain data of a different index."""


class MissingReferenceError(VerificationError):
    """A file references a target that is not part of the manifest."""


class ReplayMismatchError(VerificationError):
    """Recomputed replay digest differs from the manifest's."""


def verify_package(export_dir, replay_fn=None) -> Manifest:
    export_dir = Path(export_dir)
    raw = json.loads((export_dir / "manifest.json").read_text(encoding="utf-8"))
    embedded = raw.pop("manifest_digest", None)
    manifest = Manifest.from_dict(raw)
    if embedded != manifest.digest:
        raise ManifestTamperedError(
            f"manifest digest mismatch: embedded={embedded} recomputed={manifest.digest}"
        )
    for entry in manifest.files:
        cdir = export_dir / "chunks" / entry.file_id
        files = sorted(cdir.glob("*.chunk")) if cdir.is_dir() else []
        indices = []
        for f in files:
            try:
                indices.append(int(f.stem))
            except ValueError:
                raise ChunkOrderError(f"{entry.file_id}: unparseable chunk name {f.name}")
        if sorted(indices) != list(range(entry.chunk_count)):
            raise ChunkOrderError(
                f"{entry.file_id}: expected chunk indices 0..{entry.chunk_count - 1}, "
                f"found {sorted(indices)}"
            )
        hasher = hashlib.sha256()
        for i in range(entry.chunk_count):
            data = (cdir / f"{i:06d}.chunk").read_bytes()
            digest = sha256_hex(data)
            if digest != entry.chunk_digests[i]:
                if digest in entry.chunk_digests:
                    raise ChunkOrderError(
                        f"{entry.file_id}: chunk {i} holds data of chunk "
                        f"{entry.chunk_digests.index(digest)}"
                    )
                raise ContentTamperedError(f"{entry.file_id}: chunk {i} digest mismatch")
            hasher.update(data)
        if hasher.hexdigest() != entry.sha256:
            raise ContentTamperedError(f"{entry.file_id}: assembled file digest mismatch")
    known = {e.file_id for e in manifest.files}
    for entry in manifest.files:
        for ref in entry.references:
            if ref not in known:
                raise MissingReferenceError(f"{entry.file_id} references missing {ref}")
    if replay_fn is not None:
        domain_bytes = {}
        for entry in manifest.files:
            if entry.file_id in DOMAIN_FILES:
                cdir = export_dir / "chunks" / entry.file_id
                domain_bytes[entry.file_id] = b"".join(
                    (cdir / f"{i:06d}.chunk").read_bytes() for i in range(entry.chunk_count)
                )
        try:
            recomputed = replay_fn(domain_bytes)
        except Exception as exc:
            raise ReplayMismatchError(f"replay failed: {exc}") from exc
        if recomputed != manifest.replay_digest:
            raise ReplayMismatchError(
                f"replay digest mismatch: manifest={manifest.replay_digest} recomputed={recomputed}"
            )
    return manifest
