"""On-disk content-addressable store, staging area and cache entries.

Directory layout::

    root/
      blobs/ab/abcd....        # immutable content, keyed by sha256
      entries/<safe-key>.json  # published cache entries (create-once)
      tmp/                     # private per-build staging directories
"""

from __future__ import annotations

import hashlib
import json
import os
import shutil
import tempfile
import uuid
from dataclasses import dataclass
from pathlib import Path
from threading import Lock

from .errors import DigestMismatchError, MissingOutputError, NonDeterministicConflictError


def sha256_hex(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def _safe_entry_name(key: str) -> str:
    safe = "".join(c if c.isalnum() or c in "._-" else "_" for c in key)
    return safe.strip("_") or "key"


class ContentAddressableStore:
    """Stores immutable blobs addressed by their sha256 digest."""

    def __init__(self, root: str | Path) -> None:
        self.root = Path(root)
        self.blobs_dir = self.root / "blobs"
        self.entries_dir = self.root / "entries"
        self.tmp_dir = self.root / "tmp"
        for directory in (self.blobs_dir, self.entries_dir, self.tmp_dir):
            directory.mkdir(parents=True, exist_ok=True)
        self._blob_lock = Lock()

    def blob_path(self, digest: str) -> Path:
        return self.blobs_dir / digest[:2] / digest

    def entry_path(self, key: str) -> Path:
        return self.entries_dir / f"{_safe_entry_name(key)}.json"

    def has_blob(self, digest: str) -> bool:
        return self.blob_path(digest).is_file()

    def put_blob(self, data: bytes) -> str:
        digest = sha256_hex(data)
        target = self.blob_path(digest)
        if target.is_file():
            return digest
        with self._blob_lock:
            if target.is_file():
                return digest
            target.parent.mkdir(parents=True, exist_ok=True)
            fd, tmp_name = tempfile.mkstemp(dir=target.parent)
            try:
                with os.fdopen(fd, "wb") as handle:
                    handle.write(data)
                os.replace(tmp_name, target)
            except BaseException:
                Path(tmp_name).unlink(missing_ok=True)
                raise
        return digest

    def get_blob(self, digest: str) -> bytes:
        return self.blob_path(digest).read_bytes()

    def read_entry(self, key: str) -> dict | None:
        path = self.entry_path(key)
        if not path.is_file():
            return None
        return json.loads(path.read_text(encoding="utf-8"))

    def publish_entry(self, key: str, entry: dict) -> dict:
        """Create-once entry write.

        An existing entry with identical content is returned (dedup).  An
        existing entry with different content raises
        ``NonDeterministicConflictError``; the winner is never overwritten.
        """
        path = self.entry_path(key)
        existing = self.read_entry(key)
        if existing is not None:
            if existing.get("outputs") == entry.get("outputs"):
                return existing
            raise NonDeterministicConflictError(
                key,
                existing=existing["output_digest"],
                produced=entry["output_digest"],
            )
        payload = json.dumps(entry, indent=2, sort_keys=True).encode("utf-8")
        flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
        path.parent.mkdir(parents=True, exist_ok=True)
        try:
            fd = os.open(path, flags)
        except FileExistsError:
            # Lost a cross-process create race: compare instead of overwriting.
            winner = self.read_entry(key)
            if winner is not None and winner.get("outputs") == entry.get("outputs"):
                return winner
            existing = winner["output_digest"] if winner else "<unreadable>"
            raise NonDeterministicConflictError(
                key, existing=existing, produced=entry["output_digest"]
            )
        try:
            with os.fdopen(fd, "wb") as handle:
                handle.write(payload)
        except BaseException:
            path.unlink(missing_ok=True)
            raise
        # Re-read to guarantee concurrent creators observe the winner.
        winner = self.read_entry(key)
        if winner is not None and winner.get("outputs") != entry.get("outputs"):
            raise NonDeterministicConflictError(
                key,
                existing=winner["output_digest"],
                produced=entry["output_digest"],
            )
        return winner if winner is not None else entry


@dataclass(slots=True)
class VerifiedOutput:
    path: str
    digest: str
    size: int


class StagingArea:
    """Private per-build workspace; cleaned up on success and failure."""

    def __init__(self, store: ContentAddressableStore, key: str) -> None:
        self.store = store
        token = uuid.uuid4().hex
        self.dir = store.tmp_dir / f"{_safe_entry_name(key)}-{token}"
        self.dir.mkdir(parents=True, exist_ok=False)

    def write(self, relative_path: str, data: bytes) -> Path:
        target = (self.dir / relative_path).resolve()
        root = self.dir.resolve()
        if root not in target.parents and target != root:
            raise ValueError(f"output escapes staging directory: {relative_path!r}")
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
        return target

    def verify(self, expected: dict[str, str | None]) -> dict[str, VerifiedOutput]:
        """Verify *all* declared outputs before anything is published.

        - every declared output must exist (missing => MissingOutputError)
        - every file must be non-empty (empty => DigestMismatchError)
        - if a sha256 was declared it must match the recomputed digest
        """
        verified: dict[str, VerifiedOutput] = {}
        for relative_path, declared in expected.items():
            file_path = self.dir / relative_path
            if not file_path.is_file():
                self.cleanup()
                raise MissingOutputError(f"missing declared output: {relative_path!r}")
            data = file_path.read_bytes()
            actual = sha256_hex(data)
            if not data:
                self.cleanup()
                raise DigestMismatchError(f"empty output: {relative_path!r}")
            if declared is not None and declared.lower() != actual:
                self.cleanup()
                raise DigestMismatchError(
                    f"digest mismatch for {relative_path!r}: "
                    f"expected {declared}, got {actual}"
                )
            verified[relative_path] = VerifiedOutput(relative_path, actual, len(data))
        return verified

    def cleanup(self) -> None:
        shutil.rmtree(self.dir, ignore_errors=True)
