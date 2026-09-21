"""Content-addressed build cache.

Entries are published all-or-nothing: the caller supplies every output and
the cache records a sha256 summary (manifest) for each of them. Publishing
is atomic via a staging directory + rename. Re-publishing the same key with
identical content is deduplicated; different content is a non-deterministic
conflict and never overwrites the existing entry.
"""
from __future__ import annotations

import hashlib
import json
import shutil
import tempfile
import threading
from dataclasses import dataclass
from pathlib import Path


class NonDeterministicConflict(Exception):
    """The same key produced different artifacts; the earlier entry is kept."""

    def __init__(self, key, existing, incoming):
        self.key = key
        self.existing = dict(existing)
        self.incoming = dict(incoming)
        super().__init__(
            f"non-deterministic conflict for key {key!r}: "
            f"existing {self.existing} != incoming {self.incoming}"
        )


def _digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


@dataclass
class CacheEntry:
    key: str
    digests: dict
    path: Path

    def read_output(self, name: str) -> bytes:
        return (self.path / name).read_bytes()

    def verify(self) -> bool:
        """Summary check: every recorded output exists and matches its digest."""
        for name, expected in self.digests.items():
            artifact = self.path / name
            if not artifact.is_file() or _digest(artifact.read_bytes()) != expected:
                return False
        return True


@dataclass
class ConflictRecord:
    key: str
    existing: dict
    incoming: dict


class BuildCache:
    def __init__(self, root):
        self.root = Path(root)
        self.root.mkdir(parents=True, exist_ok=True)
        self._lock = threading.Lock()
        self.conflicts: list[ConflictRecord] = []

    def _entry_dir(self, key: str) -> Path:
        return self.root / hashlib.sha256(key.encode("utf-8")).hexdigest()

    def lookup(self, key: str):
        """Return the verified entry for key, or None when absent/corrupt."""
        manifest_path = self._entry_dir(key) / "manifest.json"
        if not manifest_path.is_file():
            return None
        data = json.loads(manifest_path.read_text("utf-8"))
        entry = CacheEntry(key=data["key"], digests=data["outputs"],
                           path=manifest_path.parent)
        if not entry.verify():
            return None
        return entry

    def publish(self, key: str, outputs: dict):
        """Atomically publish all outputs.

        Returns ``(entry, published)`` where ``published`` is False when an
        identical entry already existed (deduplication). Raises
        NonDeterministicConflict when different content already exists.
        """
        if not outputs:
            raise ValueError("refusing to publish an entry with no outputs")
        digests = {name: _digest(data) for name, data in sorted(outputs.items())}

        with self._lock:
            existing = self.lookup(key)
            if existing is not None:
                if existing.digests == digests:
                    return existing, False
                record = ConflictRecord(key, existing.digests, digests)
                self.conflicts.append(record)
                raise NonDeterministicConflict(key, existing.digests, digests)

            target = self._entry_dir(key)
            staging = Path(tempfile.mkdtemp(dir=self.root, prefix=".staging-"))
            try:
                for name, data in outputs.items():
                    (staging / name).write_bytes(data)
                (staging / "manifest.json").write_text(
                    json.dumps({"key": key, "outputs": digests},
                               indent=2, sort_keys=True),
                    "utf-8",
                )
                try:
                    staging.rename(target)
                except OSError:
                    # Lost an atomic claim to another process: re-adjudicate.
                    shutil.rmtree(staging, ignore_errors=True)
                    winner = self.lookup(key)
                    if winner is not None and winner.digests == digests:
                        return winner, False
                    record = ConflictRecord(
                        key, winner.digests if winner else {}, digests)
                    self.conflicts.append(record)
                    raise NonDeterministicConflict(
                        key, record.existing, digests) from None
            except BaseException:
                shutil.rmtree(staging, ignore_errors=True)
                raise
            return CacheEntry(key=key, digests=digests, path=target), True
