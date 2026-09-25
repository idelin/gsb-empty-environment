"""Manifest: versioned file inventory with its own overall digest."""

from __future__ import annotations

import json
from dataclasses import dataclass

from .errors import UnknownFileError
from .hashing import canonical_bytes, sha256_hex


@dataclass(frozen=True)
class FileEntry:
    file_id: str
    size: int
    sha256: str
    chunk_size: int
    chunk_digests: tuple
    references: tuple = ()

    @property
    def chunk_count(self) -> int:
        return len(self.chunk_digests)

    def to_dict(self):
        return {
            "file_id": self.file_id,
            "size": self.size,
            "sha256": self.sha256,
            "chunk_size": self.chunk_size,
            "chunk_digests": list(self.chunk_digests),
            "references": list(self.references),
        }

    @classmethod
    def from_dict(cls, d):
        return cls(
            d["file_id"],
            int(d["size"]),
            d["sha256"],
            int(d["chunk_size"]),
            tuple(d["chunk_digests"]),
            tuple(d.get("references", ())),
        )


@dataclass(frozen=True)
class Manifest:
    package_id: str
    version: int
    replay_digest: str
    files: tuple

    def to_dict(self):
        return {
            "package_id": self.package_id,
            "version": self.version,
            "replay_digest": self.replay_digest,
            "files": [f.to_dict() for f in self.files],
        }

    @property
    def digest(self) -> str:
        """Overall digest of the manifest itself (version + all entries)."""
        return sha256_hex(canonical_bytes(self.to_dict()))

    def to_json(self) -> str:
        return json.dumps({**self.to_dict(), "manifest_digest": self.digest}, indent=2, sort_keys=True)

    @classmethod
    def from_dict(cls, d):
        return cls(
            d["package_id"],
            int(d["version"]),
            d["replay_digest"],
            tuple(FileEntry.from_dict(x) for x in d["files"]),
        )

    @classmethod
    def from_json(cls, text: str):
        d = json.loads(text)
        d.pop("manifest_digest", None)
        return cls.from_dict(d)

    def file_entry(self, file_id: str) -> FileEntry:
        for entry in self.files:
            if entry.file_id == file_id:
                return entry
        raise UnknownFileError(f"{file_id} is not part of manifest {self.package_id} v{self.version}")
