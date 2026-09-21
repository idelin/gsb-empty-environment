"""Content-addressed multi-output build cache with atomic publication.

Publication protocol for one cache key:

1. Every declared output must exist in the staging directory
   ("one output missing => publish nothing").
2. Every staged output is copied into a private per-attempt staging area and
   its SHA-256 digest is computed; the digest is recomputed immediately before
   commit so a tampered staging tree cannot be published.
3. If the cache key is absent, the entry directory is claimed atomically
   (``mkdir``) and all outputs are renamed into it, then verified again.
   If the key already exists:
     * identical digests  -> deduplication (nothing is written);
     * different digests  -> NonDeterministicConflict (the first entry wins,
       no overwrite is ever allowed).
4. Any failure purges the private staging area; a failed attempt never leaves
   a partial entry behind.

The on-disk locking makes concurrent builds safe within one process; the
atomic ``mkdir`` claim additionally guards separate processes sharing a cache.
"""

from __future__ import annotations

import enum
import json
import os
import shutil
import threading
import uuid
from datetime import datetime, timezone
from pathlib import Path
from typing import Dict, Mapping, NamedTuple, Optional, Sequence, Tuple

from .digest import canonical_json, sha256_bytes, short
from .version import Version

ENTRY_FILE = "entry.json"
TMP_PREFIX = ".staging-"
_NAME_BAD = {"", ".", ".."}


class CacheError(RuntimeError):
    """Base class for cache publication errors."""


class MissingOutputError(CacheError):
    """One or more declared outputs were not found in the staged build."""

    def __init__(self, key: str, missing: Sequence[str]):
        self.key = key
        self.missing = tuple(missing)
        super().__init__(
            f"refusing to publish {short(key)}: missing outputs: {', '.join(sorted(self.missing))}"
        )


class IntegrityError(CacheError):
    """A staged output changed between digesting and publication."""


class NonDeterministicConflict(CacheError):
    """Same cache key, different artifacts. The existing entry always wins."""

    def __init__(self, key: str, diffs: Mapping[str, Tuple[str, str]]):
        self.key = key
        self.diffs = dict(diffs)
        details = "; ".join(
            f"{name}: existing {short(old)} != new {short(new)}"
            for name, (old, new) in sorted(self.diffs.items())
        )
        super().__init__(
            f"non-deterministic build for key {short(key)} ({details}); "
            "keeping existing entry, overwrite rejected"
        )


class PublishResult(enum.Enum):
    PUBLISHED = "published"
    DEDUPLICATED = "deduplicated"


class OutputDigest(NamedTuple):
    name: str
    digest: str
    size: int


def _safe_output_name(name: str) -> str:
    if not isinstance(name, str) or name in _NAME_BAD or "\x00" in name:
        raise CacheError(f"invalid output name: {name!r}")
    normalized = Path(name)
    if normalized.is_absolute() or ".." in normalized.parts or os.path.isabs(name):
        raise CacheError(f"output name must be a relative path without '..': {name!r}")
    return normalized.as_posix()


def _utc_now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def _atomic_write_json(path: Path, payload: object) -> None:
    tmp = path.with_name(path.name + ".tmp")
    tmp.write_bytes(canonical_json(payload) + b"\n")
    os.replace(tmp, path)


class BuildCache:
    """A directory backed cache of multi-output build entries."""

    def __init__(self, root: os.PathLike[str] | str) -> None:
        self.root = Path(root)
        self.entries_dir = self.root / "entries"
        self.tmp_dir = self.root / "tmp"
        self._global_lock = threading.RLock()
        self._key_locks: Dict[str, threading.Lock] = {}
        self.entries_dir.mkdir(parents=True, exist_ok=True)
        self.tmp_dir.mkdir(parents=True, exist_ok=True)

    # ------------------------------------------------------------------ paths
    def entry_path(self, key: str) -> Path:
        return self.entries_dir / key

    def _key_lock(self, key: str) -> threading.Lock:
        with self._global_lock:
            lock = self._key_locks.get(key)
            if lock is None:
                lock = threading.Lock()
                self._key_locks[key] = lock
            return lock

    # ------------------------------------------------------------- inspection
    def has(self, key: str) -> bool:
        return (self.entry_path(key) / ENTRY_FILE).is_file()

    def get(self, key: str) -> Optional[dict]:
        record = self.entry_path(key) / ENTRY_FILE
        if not record.is_file():
            return None
        return json.loads(record.read_text(encoding="utf-8"))

    def output_path(self, key: str, output: str) -> Path:
        return self.entry_path(key) / _safe_output_name(output)

    def keys(self) -> list[str]:
        if not self.entries_dir.is_dir():
            return []
        return sorted(
            child.name
            for child in self.entries_dir.iterdir()
            if (child / ENTRY_FILE).is_file()
        )

    # ------------------------------------------------------------- staging
    def _stage_build(
        self,
        key: str,
        staged_dir: os.PathLike[str] | str,
        declared_outputs: Sequence[str],
        attempt: str,
    ) -> Tuple[Path, dict[str, OutputDigest]]:
        """Copy all declared outputs into a private staging area, digested."""
        staged_dir = Path(staged_dir)
        names = [_safe_output_name(name) for name in declared_outputs]
        missing = [name for name in names if not (staged_dir / name).is_file()]
        if missing:
            raise MissingOutputError(key, missing)

        token = uuid.uuid4().hex[:8]
        area = self.tmp_dir / f"{TMP_PREFIX}{short(key, 8)}-{attempt}-{token}"
        if area.exists():
            shutil.rmtree(area)
        area.mkdir(parents=True)

        digests: dict[str, OutputDigest] = {}
        try:
            for name in names:
                src = staged_dir / name
                dst = area / name
                dst.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(src, dst, follow_symlinks=False)
                data = dst.read_bytes()
                digests[name] = OutputDigest(name, sha256_bytes(data), len(data))
        except OSError as exc:
            shutil.rmtree(area, ignore_errors=True)
            raise CacheError(f"failed staging outputs for {short(key)}: {exc}") from exc
        return area, digests

    @staticmethod
    def _verify_area(area: Path, digests: Mapping[str, OutputDigest]) -> None:
        for name, expected in digests.items():
            path = area / name
            data = path.read_bytes()
            actual = sha256_bytes(data)
            if actual != expected.digest or len(data) != expected.size:
                raise IntegrityError(
                    f"staged output {name!r} changed before commit: "
                    f"expected {short(expected.digest)}, found {short(actual)}"
                )

    def _manifest(
        self,
        key: str,
        package: str,
        version: object,
        inputs: object,
        digests: Mapping[str, OutputDigest],
    ) -> dict:
        outputs = {
            name: {"sha256": meta.digest, "size": meta.size}
            for name, meta in sorted(digests.items())
        }
        return {
            "schema": 1,
            "key": key,
            "package": package,
            "version": str(version),
            "prerelease": Version.parse(version).prerelease,
            "outputs": outputs,
            "input_digest": sha256_bytes(canonical_json(_sorted(inputs))),
            "created_at": _utc_now(),
        }


    # ------------------------------------------------------------- commit
    def _commit(
        self,
        area: Path,
        key: str,
        manifest: dict,
        digests: Mapping[str, OutputDigest],
    ) -> PublishResult:
        target = self.entry_path(key)
        lock = self._key_lock(key)
        with lock:
            existing_record = target / ENTRY_FILE
            if existing_record.is_file():
                existing = json.loads(existing_record.read_text(encoding="utf-8"))
                diffs = {
                    name: (
                        existing["outputs"].get(name, {}).get("sha256", "<missing>"),
                        meta.digest,
                    )
                    for name, meta in digests.items()
                    if existing["outputs"].get(name, {}).get("sha256") != meta.digest
                }
                if not diffs:
                    # Same key, same artifacts: de-duplicate, write nothing.
                    return PublishResult.DEDUPLICATED
                raise NonDeterministicConflict(key, diffs)

            # Re-verify the private staging area right before claiming the key.
            self._verify_area(area, digests)

            claimed = self._claim(target)
            if not claimed:
                # Another process won the race after our in-process check.
                existing = json.loads(existing_record.read_text(encoding="utf-8"))
                diffs = {
                    name: (
                        existing["outputs"].get(name, {}).get("sha256", "<missing>"),
                        meta.digest,
                    )
                    for name, meta in digests.items()
                    if existing["outputs"].get(name, {}).get("sha256") != meta.digest
                }
                if not diffs:
                    return PublishResult.DEDUPLICATED
                raise NonDeterministicConflict(key, diffs)

            try:
                for name in digests:
                    src = area / name
                    dst = target / name
                    dst.parent.mkdir(parents=True, exist_ok=True)
                    os.replace(src, dst)
                _atomic_write_json(target / ENTRY_FILE, manifest)
            except BaseException:
                # Never leave a half-populated entry behind.
                shutil.rmtree(target, ignore_errors=True)
                raise

            # Post-commit verification: nothing is "published" that fails a
            # digest check.
            for name, expected in digests.items():
                data = (target / name).read_bytes()
                if sha256_bytes(data) != expected.digest:
                    shutil.rmtree(target, ignore_errors=True)
                    raise IntegrityError(
                        f"output {name!r} failed post-commit digest check"
                    )
            return PublishResult.PUBLISHED

    @staticmethod
    def _claim(target: Path) -> bool:
        """Atomically claim an entry directory. False if another process did."""
        while True:
            try:
                target.mkdir(parents=True, exist_ok=False)
                return True
            except FileExistsError:
                if (target / ENTRY_FILE).is_file():
                    return False
                # A crashed attempt may have left a claim; entry dirs are only
                # ever created by us together with the rename sequence, so a
                # directory without a record is stale.
                try:
                    os.rmdir(target)
                except OSError:
                    return False

    # ------------------------------------------------------------- public API
    def publish(
        self,
        key: str,
        staged_dir: os.PathLike[str] | str,
        *,
        package: str,
        version: object,
        inputs: object,
        outputs: Sequence[str],
        attempt: str = "0",
    ) -> PublishResult:
        """Publish all declared outputs for *key* or nothing at all."""
        area: Optional[Path] = None
        try:
            area, digests = self._stage_build(key, staged_dir, outputs, attempt)
            manifest = self._manifest(key, package, version, inputs, digests)
            return self._commit(area, key, manifest, digests)
        finally:
            if area is not None:
                shutil.rmtree(area, ignore_errors=True)

    def verify_entry(self, key: str) -> bool:
        """Return True when every recorded output exists with matching digest."""
        record = self.get(key)
        if record is None:
            return False
        for name, meta in record["outputs"].items():
            path = self.entry_path(key) / name
            if not path.is_file():
                return False
            data = path.read_bytes()
            if sha256_bytes(data) != meta["sha256"] or len(data) != meta["size"]:
                return False
        return True


def _sorted(value):
    if isinstance(value, Mapping):
        return {str(k): _sorted(value[k]) for k in sorted(value, key=str)}
    if isinstance(value, (list, tuple)):
        return [_sorted(item) for item in value]
    return value
