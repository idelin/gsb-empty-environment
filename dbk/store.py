from __future__ import annotations

import hashlib
import json
import os
import uuid
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from .vfs import STATE_DIR_NAME, VFS


class SimulatedCrash(RuntimeError):
    pass


@dataclass(frozen=True)
class StagedOutput:
    path: Path
    digest: str


def _fsync_directory(path: Path) -> None:
    descriptor = os.open(path, os.O_RDONLY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


class ArtifactStore:
    """Content-addressed objects, atomic publication and append-only local logs."""

    def __init__(
        self,
        vfs: VFS,
        crash_points: set[str] | None = None,
        crash_once: bool = True,
    ):
        self.vfs = vfs
        self.state_dir = vfs.absolute(STATE_DIR_NAME)
        self.object_dir = self.state_dir / "objects"
        self.tmp_dir = self.state_dir / "tmp"
        self.wal_path = self.state_dir / "wal.jsonl"
        self.event_path = self.state_dir / "events.jsonl"
        for directory in (self.state_dir, self.object_dir, self.tmp_dir):
            directory.mkdir(parents=True, exist_ok=True)
        self.crash_points = set(crash_points or ())
        self.crash_once = crash_once

    def crash(self, point: str) -> None:
        if point not in self.crash_points:
            return
        if self.crash_once:
            self.crash_points.remove(point)
        raise SimulatedCrash(f"simulated crash at {point}")

    def stage_output(self, data: bytes) -> StagedOutput:
        digest = hashlib.sha256(data).hexdigest()
        staged = self.tmp_dir / f"output-{uuid.uuid4().hex}"
        staged.write_bytes(data)
        with staged.open("rb") as stream:
            os.fsync(stream.fileno())
        return StagedOutput(staged, digest)

    def read_wal(self) -> list[dict[str, Any]]:
        if not self.wal_path.exists():
            return []
        records: list[dict[str, Any]] = []
        for line in self.wal_path.read_text("utf-8").splitlines():
            if line.strip():
                records.append(json.loads(line))
        return records

    def append_wal(self, record: dict[str, Any]) -> None:
        records = self.read_wal()
        records.append(record)
        self._atomic_write_jsonl(self.wal_path, records)

    def rewrite_wal(self, records: list[dict[str, Any]]) -> None:
        self._atomic_write_jsonl(self.wal_path, records)

    def append_event(self, record: dict[str, Any]) -> None:
        records = []
        if self.event_path.exists():
            for line in self.event_path.read_text("utf-8").splitlines():
                if line.strip():
                    records.append(json.loads(line))
        records.append(record)
        self._atomic_write_jsonl(self.event_path, records)

    def read_events(self) -> list[dict[str, Any]]:
        if not self.event_path.exists():
            return []
        return [
            json.loads(line)
            for line in self.event_path.read_text("utf-8").splitlines()
            if line.strip()
        ]

    def _atomic_write_jsonl(self, path: Path, records: list[dict[str, Any]]) -> None:
        temporary = path.with_name(f".{path.name}.{uuid.uuid4().hex}")
        payload = "".join(json.dumps(record, sort_keys=True) + "\n" for record in records)
        temporary.write_text(payload, "utf-8")
        with temporary.open("rb") as stream:
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        _fsync_directory(path.parent)

    def commit_object(self, staged: StagedOutput) -> str:
        target = self.object_dir / staged.digest
        if not target.exists():
            os.replace(staged.path, target)
            _fsync_directory(self.object_dir)
        else:
            staged.path.unlink(missing_ok=True)
        return staged.digest

    def object_path(self, digest: str) -> Path:
        return self.object_dir / digest

    def object_exists(self, digest: str) -> bool:
        return (self.object_dir / digest).is_file()

    def read_object(self, digest: str) -> bytes:
        return (self.object_dir / digest).read_bytes()

    def publish(self, output_path: str, digest: str) -> None:
        target = self.vfs.absolute(output_path)
        target.parent.mkdir(parents=True, exist_ok=True)
        temporary = target.parent / f".{target.name}.link.{uuid.uuid4().hex}"
        object_absolute = self.object_path(digest).resolve()
        os.symlink(object_absolute, temporary)
        os.replace(temporary, target)
        _fsync_directory(target.parent)

    def published_digest(self, output_path: str) -> str | None:
        target = self.vfs.absolute(output_path)
        if not target.is_symlink() or not target.is_file():
            return None
        try:
            with target.open("rb") as stream:
                return hashlib.file_digest(stream, "sha256").hexdigest()
        except (FileNotFoundError, OSError):
            return None

    def cleanup_temporary(self) -> None:
        for child in self.tmp_dir.iterdir():
            child.unlink(missing_ok=True)
