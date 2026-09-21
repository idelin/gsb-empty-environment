"""Content-addressed cache store with dedupe and conflict detection."""

from __future__ import annotations

import hashlib
import json
import os
import threading
from dataclasses import dataclass
from pathlib import Path


class ConflictError(Exception):
    """Same key produced different artifacts: non-deterministic build."""

    def __init__(self, key: str, existing_digest: str, incoming_digest: str):
        super().__init__(
            f"non-deterministic build for key {key[:12]}...: "
            f"cached digest {existing_digest[:12]}... != new digest {incoming_digest[:12]}..."
        )
        self.key = key


@dataclass(frozen=True)
class CacheEntry:
    key: str
    digest: str
    outputs: dict[str, str]


class CacheStore:
    def __init__(self, root: str | Path):
        self.root = Path(root)
        self.entries_dir = self.root / "entries"
        self.objects_dir = self.root / "objects"
        self.entries_dir.mkdir(parents=True, exist_ok=True)
        self.objects_dir.mkdir(parents=True, exist_ok=True)
        self._lock = threading.Lock()

    def _entry_path(self, key: str) -> Path:
        return self.entries_dir / f"{key}.json"

    def get(self, key: str) -> CacheEntry | None:
        path = self._entry_path(key)
        if not path.exists():
            return None
        data = json.loads(path.read_text(encoding="utf-8"))
        return CacheEntry(key=data["key"], digest=data["digest"], outputs=dict(data["outputs"]))

    def publish(self, entry: CacheEntry, artifacts: dict[str, bytes]) -> CacheEntry:
        with self._lock:
            existing = self.get(entry.key)
            if existing is not None:
                if existing.digest == entry.digest and existing.outputs == entry.outputs:
                    return existing
                raise ConflictError(entry.key, existing.digest, entry.digest)
            for data in artifacts.values():
                self._store_object(data)
            tmp = self.entries_dir / f"{entry.key}.{os.getpid()}.tmp"
            tmp.write_text(
                json.dumps(
                    {"key": entry.key, "digest": entry.digest, "outputs": entry.outputs},
                    indent=2,
                    sort_keys=True,
                ),
                encoding="utf-8",
            )
            os.replace(tmp, self._entry_path(entry.key))
            return entry

    def _store_object(self, data: bytes) -> None:
        obj = self.objects_dir / hashlib.sha256(data).hexdigest()
        if obj.exists():
            return
        tmp = obj.with_name(f"{obj.name}.{os.getpid()}.tmp")
        tmp.write_bytes(data)
        os.replace(tmp, obj)
