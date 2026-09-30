"""Bounded-memory external merge sort with crash-safe, manifest-based resume.

Records are ``(key, seq, value)`` triples, where ``seq`` is the record's
0-based ordinal in the original input.  Sorting is by ``(key, seq)``, which
makes the sort stable: equal keys keep their original input order no matter
how the runs are split or how many merge rounds occur.

Durability model
----------------
* Every temporary run file is written to a ``.tmp`` sibling, fsynced and
  atomically ``os.replace``-d into place, then hashed (SHA-256).
* After each durable run file appears, the manifest (record counts, run
  hashes, input offset/hash, merge round) is rewritten atomically the same
  way.  The manifest itself carries a SHA-256 checksum of its payload.
* On resume, the manifest checksum is verified and every referenced run file
  is re-hashed and re-counted.  Any mismatch raises :class:`CorruptRunError`
  or :class:`ManifestCorruptError`` -- corrupt state is never silently
  dropped.
* The final output is written to a temp file in the output directory,
  fsynced, verified for record-count conservation, then atomically
  ``os.replace``-d over the destination.

Because runs are immutable once published to the manifest and the final
replace is atomic, re-running after an interruption can never duplicate
records: the output record count always equals the input record count.
"""

from __future__ import annotations

import hashlib
import heapq
import json
import os
from typing import Any, BinaryIO, Iterator

MANIFEST_NAME = "manifest.json"
MANIFEST_VERSION = 1
_RUN_GLOB_SUFFIXES = (".run", ".run.tmp")
# File handles reserved during a merge: one merged-output file plus one
# manifest temp file (the manifest may be rewritten while output is open).
_RESERVED_HANDLES = 2
_CHUNK = 1 << 20


class CorruptRunError(RuntimeError):
    """A temporary run file failed validation; it is never silently dropped."""


class ManifestCorruptError(RuntimeError):
    """The resume manifest is malformed or fails its checksum."""


class SortInterrupted(Exception):
    """Test hook: simulates a crash after a durable state write."""


def _file_sha256(path: str, limit: int | None = None) -> str:
    """SHA-256 of a file, or of its first ``limit`` bytes when given."""
    hasher = hashlib.sha256()
    remaining = limit
    with open(path, "rb") as handle:
        while remaining is None or remaining > 0:
            want = _CHUNK if remaining is None else min(_CHUNK, remaining)
            chunk = handle.read(want)
            if not chunk:
                break
            hasher.update(chunk)
            if remaining is not None:
                remaining -= len(chunk)
    return hasher.hexdigest()


def _hash_prefix(path: str, offset: int) -> "hashlib._Hash":
    """Return an incremental hasher primed with the first ``offset`` bytes."""
    hasher = hashlib.sha256()
    remaining = offset
    with open(path, "rb") as handle:
        while remaining > 0:
            chunk = handle.read(min(_CHUNK, remaining))
            if not chunk:
                break
            hasher.update(chunk)
            remaining -= len(chunk)
    return hasher


def _count_lines(path: str) -> int:
    count = 0
    with open(path, "rb") as handle:
        for _ in handle:
            count += 1
    return count


class _RunWriter:
    """Streams records to one run file and publishes it atomically."""

    def __init__(self, workdir: str, name: str) -> None:
        self._name = name
        self._path = os.path.join(workdir, name)
        self._count = 0
        self._handle = open(self._path + ".tmp", "w", encoding="utf-8")

    def write(self, record: tuple) -> None:
        self._handle.write(json.dumps(list(record), separators=(",", ":")))
        self._handle.write("\n")
        self._count += 1

    def close(self) -> dict:
        self._handle.flush()
        os.fsync(self._handle.fileno())
        self._handle.close()
        os.replace(self._path + ".tmp", self._path)
        return {
            "path": self._name,
            "count": self._count,
            "sha256": _file_sha256(self._path),
        }


class ExternalSorter:
    """Sort a JSONL input file by key with bounded memory and resume support.

    Parameters
    ----------
    input_path, output_path:
        Input/output JSONL files (see README for the record format).
    workdir:
        Directory for run files and the resume manifest.
    memory_budget:
        Maximum number of records held in memory at once (run size).
    handle_budget:
        Maximum simultaneously open file handles; merge fan-in is
        ``handle_budget - 2`` (output file + manifest temp are reserved).
    keep_work:
        Keep run files and manifest after a successful sort (debugging).
    interrupt_after:
        Test hook: raise :class:`SortInterrupted` after this many durable
        state writes, simulating a crash.  ``None`` disables the hook.
    """

    def __init__(
        self,
        input_path: str,
        output_path: str,
        workdir: str,
        memory_budget: int = 10000,
        handle_budget: int = 32,
        keep_work: bool = False,
        interrupt_after: int | None = None,
    ) -> None:
        if memory_budget < 1:
            raise ValueError("memory_budget must be >= 1 record")
        fan_in = handle_budget - _RESERVED_HANDLES
        if fan_in < 2:
            raise ValueError(
                f"handle_budget={handle_budget} leaves fan-in {fan_in} < 2 "
                f"(need at least {_RESERVED_HANDLES} reserved handles)"
            )
        self.input_path = input_path
        self.output_path = output_path
        self.workdir = workdir
        self.memory_budget = memory_budget
        self.handle_budget = handle_budget
        self.fan_in = fan_in
        self.keep_work = keep_work
        self._writes_until_interrupt = interrupt_after
        self._manifest_path = os.path.join(workdir, MANIFEST_NAME)

    # ------------------------------------------------------------------ API

    def sort(self) -> int:
        """Run (or resume) the sort; return the number of output records."""
        os.makedirs(self.workdir, exist_ok=True)
        state = self._load_state()
        if not state["input_done"]:
            self._split_pass(state)
        while len(state["runs"]) > self.fan_in:
            self._merge_round(state)
        self._final_merge(state)
        if not self.keep_work:
            self._cleanup(state)
        return state["records_read"]

    # ------------------------------------------------------------- manifest

    def _initial_state(self) -> dict:
        return {
            "version": MANIFEST_VERSION,
            "records_read": 0,
            "input_offset": 0,
            "input_prefix_sha256": hashlib.sha256(b"").hexdigest(),
            "input_done": False,
            "round": 0,
            "run_counter": 0,
            "runs": [],
        }

    def _load_state(self) -> dict:
        if not os.path.exists(self._manifest_path):
            return self._initial_state()
        try:
            with open(self._manifest_path, "r", encoding="utf-8") as handle:
                doc = json.load(handle)
        except (OSError, json.JSONDecodeError) as exc:
            raise ManifestCorruptError(
                f"cannot read manifest {self._manifest_path}: {exc}"
            ) from exc
        payload = doc.get("payload") if isinstance(doc, dict) else None
        checksum = doc.get("sha256") if isinstance(doc, dict) else None
        if not isinstance(payload, dict) or not isinstance(checksum, str):
            raise ManifestCorruptError("manifest is missing payload or checksum")
        blob = json.dumps(payload, sort_keys=True, separators=(",", ":")).encode()
        if hashlib.sha256(blob).hexdigest() != checksum:
            raise ManifestCorruptError("manifest checksum mismatch")
        if payload.get("version") != MANIFEST_VERSION:
            raise ManifestCorruptError(
                f"unsupported manifest version: {payload.get('version')!r}"
            )
        self._validate_runs(payload["runs"])
        expected = sum(entry["count"] for entry in payload["runs"])
        if expected != payload["records_read"]:
            raise ManifestCorruptError(
                f"manifest inconsistency: runs hold {expected} records but "
                f"{payload['records_read']} were read from input"
            )
        return payload

    def _save_manifest(self, state: dict) -> None:
        blob = json.dumps(state, sort_keys=True, separators=(",", ":")).encode()
        doc = {"payload": state, "sha256": hashlib.sha256(blob).hexdigest()}
        tmp = self._manifest_path + ".tmp"
        with open(tmp, "w", encoding="utf-8") as handle:
            json.dump(doc, handle)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(tmp, self._manifest_path)

    def _validate_runs(self, runs: list[dict]) -> None:
        for entry in runs:
            path = os.path.join(self.workdir, entry["path"])
            if not os.path.exists(path):
                raise CorruptRunError(f"run file missing: {path}")
            if _file_sha256(path) != entry["sha256"]:
                raise CorruptRunError(f"run file checksum mismatch: {path}")
            if _count_lines(path) != entry["count"]:
                raise CorruptRunError(f"run file record count mismatch: {path}")

    # ---------------------------------------------------------------- phases

    def _interrupt_hook(self) -> None:
        if self._writes_until_interrupt is not None:
            self._writes_until_interrupt -= 1
            if self._writes_until_interrupt <= 0:
                raise SortInterrupted("simulated crash (test hook)")

    def _next_run_name(self, state: dict) -> str:
        name = f"run-{state['round']:03d}-{state['run_counter']:06d}.run"
        state["run_counter"] += 1
        return name

    def _flush_buffer(self, buffer: list[tuple], state: dict) -> None:
        buffer.sort(key=lambda rec: (rec[0], rec[1]))
        writer = _RunWriter(self.workdir, self._next_run_name(state))
        for record in buffer:
            writer.write(record)
        state["runs"].append(writer.close())
        self._save_manifest(state)
        self._interrupt_hook()

    def _split_pass(self, state: dict) -> None:
        offset = state["input_offset"]
        hasher = _hash_prefix(self.input_path, offset)
        if hasher.hexdigest() != state["input_prefix_sha256"]:
            raise ManifestCorruptError(
                "input file changed since the interrupted run; refusing to resume"
            )
        buffer: list[tuple] = []
        with open(self.input_path, "rb") as handle:
            handle.seek(offset)
            for raw in handle:
                hasher.update(raw)
                offset += len(raw)
                record = json.loads(raw.decode("utf-8"))
                buffer.append((record["key"], state["records_read"], record.get("value")))
                state["records_read"] += 1
                if len(buffer) >= self.memory_budget:
                    state["input_offset"] = offset
                    state["input_prefix_sha256"] = hasher.hexdigest()
                    self._flush_buffer(buffer, state)
                    buffer = []
            state["input_offset"] = offset
            state["input_prefix_sha256"] = hasher.hexdigest()
        if buffer:
            self._flush_buffer(buffer, state)
        state["input_done"] = True
        self._save_manifest(state)

    def _read_run(self, entry: dict) -> Iterator[tuple]:
        path = os.path.join(self.workdir, entry["path"])
        with open(path, "r", encoding="utf-8") as handle:
            for lineno, line in enumerate(handle, 1):
                try:
                    record = json.loads(line)
                    key, seq, value = record
                except (json.JSONDecodeError, ValueError, TypeError) as exc:
                    raise CorruptRunError(
                        f"{path}:{lineno}: undecodable record"
                    ) from exc
                yield (key, seq, value)

    def _merge_batch(self, batch: list[dict], state: dict) -> dict:
        streams = [self._read_run(entry) for entry in batch]
        writer = _RunWriter(self.workdir, self._next_run_name(state))
        count = 0
        for record in heapq.merge(*streams, key=lambda rec: (rec[0], rec[1])):
            writer.write(record)
            count += 1
        entry = writer.close()
        expected = sum(item["count"] for item in batch)
        if count != expected:
            raise CorruptRunError(
                f"merge produced {count} records, expected {expected}"
            )
        return entry

    def _merge_round(self, state: dict) -> None:
        state["round"] += 1
        remaining = list(state["runs"])
        merged: list[dict] = []
        while remaining:
            batch, remaining = remaining[: self.fan_in], remaining[self.fan_in :]
            merged.append(self._merge_batch(batch, state))
            state["runs"] = remaining + merged
            self._save_manifest(state)
            for old in batch:
                os.unlink(os.path.join(self.workdir, old["path"]))
            self._interrupt_hook()
        total = sum(entry["count"] for entry in state["runs"])
        if total != state["records_read"]:
            raise CorruptRunError(
                f"record count changed during merge: {total} != "
                f"{state['records_read']}"
            )

    def _final_merge(self, state: dict) -> None:
        if not state["input_done"]:
            raise ManifestCorruptError("input not fully consumed before final merge")
        out_dir = os.path.dirname(os.path.abspath(self.output_path))
        os.makedirs(out_dir, exist_ok=True)
        tmp = os.path.join(
            out_dir, os.path.basename(self.output_path) + f".tmp-{os.getpid()}"
        )
        streams = [self._read_run(entry) for entry in state["runs"]]
        count = 0
        with open(tmp, "w", encoding="utf-8") as handle:
            for key, seq, value in heapq.merge(
                *streams, key=lambda rec: (rec[0], rec[1])
            ):
                handle.write(
                    json.dumps(
                        {"key": key, "seq": seq, "value": value},
                        separators=(",", ":"),
                    )
                )
                handle.write("\n")
                count += 1
            handle.flush()
            os.fsync(handle.fileno())
        if count != state["records_read"]:
            os.unlink(tmp)
            raise CorruptRunError(
                f"final output would hold {count} records, expected "
                f"{state['records_read']}; output left untouched"
            )
        os.replace(tmp, self.output_path)

    def _cleanup(self, state: dict) -> None:
        for entry in state["runs"]:
            try:
                os.unlink(os.path.join(self.workdir, entry["path"]))
            except OSError:
                pass
        for name in os.listdir(self.workdir):
            if name.endswith(_RUN_GLOB_SUFFIXES) or name.startswith(MANIFEST_NAME):
                try:
                    os.unlink(os.path.join(self.workdir, name))
                except OSError:
                    pass
        try:
            os.rmdir(self.workdir)
        except OSError:
            pass
