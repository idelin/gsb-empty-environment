"""Manifest model with a stable version and an overall content digest."""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass
from typing import BinaryIO, Iterator


def sha256_hex(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def iter_blocks(data: bytes, chunk_size: int) -> Iterator[bytes]:
    if chunk_size <= 0:
        raise ValueError("chunk_size must be positive")
    for start in range(0, len(data), chunk_size):
        yield data[start:start + chunk_size]


def iter_file_blocks(stream: BinaryIO, chunk_size: int) -> Iterator[bytes]:
    """Memory-bounded read: yields at most one chunk at a time."""
    if chunk_size <= 0:
        raise ValueError("chunk_size must be positive")
    while True:
        block = stream.read(chunk_size)
        if not block:
            return
        yield block


@dataclass(frozen=True)
class FileEntry:
    file_id: str
    size: int
    chunk_size: int
    chunk_digests: tuple[str, ...]
    file_digest: str

    @property
    def chunk_count(self) -> int:
        return len(self.chunk_digests)

    def to_dict(self) -> dict:
        return {
            "file_id": self.file_id,
            "size": self.size,
            "chunk_size": self.chunk_size,
            "chunk_digests": list(self.chunk_digests),
            "file_digest": self.file_digest,
        }

    @classmethod
    def from_dict(cls, body: dict) -> "FileEntry":
        return cls(
            file_id=body["file_id"],
            size=body["size"],
            chunk_size=body["chunk_size"],
            chunk_digests=tuple(body["chunk_digests"]),
            file_digest=body["file_digest"],
        )

    @classmethod
    def from_bytes(cls, file_id: str, data: bytes, chunk_size: int) -> "FileEntry":
        digests = tuple(sha256_hex(block) for block in iter_blocks(data, chunk_size))
        return cls(
            file_id=file_id,
            size=len(data),
            chunk_size=chunk_size,
            chunk_digests=digests,
            file_digest=sha256_hex(data),
        )


@dataclass(frozen=True)
class Reference:
    source_file_id: str
    target_file_id: str
    target_digest: str

    def to_dict(self) -> dict:
        return {
            "source_file_id": self.source_file_id,
            "target_file_id": self.target_file_id,
            "target_digest": self.target_digest,
        }

    @classmethod
    def from_dict(cls, body: dict) -> "Reference":
        return cls(
            source_file_id=body["source_file_id"],
            target_file_id=body["target_file_id"],
            target_digest=body["target_digest"],
        )


@dataclass(frozen=True)
class Manifest:
    manifest_id: str
    version: int
    files: tuple[FileEntry, ...]
    references: tuple[Reference, ...]

    def __post_init__(self) -> None:
        ids = [entry.file_id for entry in self.files]
        if ids != sorted(ids):
            raise ValueError("manifest files must be canonically ordered by file_id")
        if len(set(ids)) != len(ids):
            raise ValueError("duplicate file_id in manifest")

    @property
    def digest(self) -> str:
        return sha256_hex(self.canonical_bytes())

    def canonical_bytes(self) -> bytes:
        return json.dumps(
            self.to_dict(), sort_keys=True, separators=(",", ":")
        ).encode("utf-8")

    def to_dict(self) -> dict:
        return {
            "manifest_id": self.manifest_id,
            "version": self.version,
            "files": [entry.to_dict() for entry in self.files],
            "references": [ref.to_dict() for ref in self.references],
        }

    @classmethod
    def from_dict(cls, body: dict) -> "Manifest":
        return cls(
            manifest_id=body["manifest_id"],
            version=body["version"],
            files=tuple(FileEntry.from_dict(item) for item in body["files"]),
            references=tuple(Reference.from_dict(item) for item in body["references"]),
        )

    def file_entry(self, file_id: str) -> FileEntry | None:
        for entry in self.files:
            if entry.file_id == file_id:
                return entry
        return None


def build_manifest(
    manifest_id: str,
    version: int,
    files: dict[str, bytes],
    references: list[tuple[str, str]] | None = None,
    chunk_size: int = 64 * 1024,
) -> Manifest:
    entries = tuple(
        FileEntry.from_bytes(file_id, data, chunk_size)
        for file_id, data in sorted(files.items())
    )
    refs = tuple(
        Reference(source_file_id=src, target_file_id=dst,
                  target_digest=sha256_hex(files[dst]))
        for src, dst in (references or [])
    )
    return Manifest(manifest_id=manifest_id, version=version,
                    files=entries, references=refs)
