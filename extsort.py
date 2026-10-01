#!/usr/bin/env python3
"""extsort -- finite-memory external merge sort with crash recovery.

Records are (key, seq, value) triples, where ``seq`` is the record's index in
the original input.  Sorting is by (key, seq), so the sort is stable: equal
keys keep their original input order.

Properties
----------
* Memory budget is counted in records: at most ``memory_records`` records are
  held in memory at once (one in-memory run, or one output buffer record
  during merging; merging itself is a streaming k-way cursor).
* Merge fan-in is bounded by ``fan_in`` (the file-handle budget): never more
  than ``fan_in`` segment files are open at the same time.
* Crash recovery: every completed segment is registered in an atomically
  replaced, checksummed manifest.  Re-running after an interruption resumes
  from the manifest instead of starting over.
* Corrupt temporary segments are never silently dropped: a missing file, a
  checksum mismatch, an unparsable line, or a record-count mismatch raises
  :class:`CorruptSegmentError`.
* The final output is written to a temporary file in the same directory and
  atomically moved into place with ``os.replace``.
"""

from __future__ import annotations

import argparse
import hashlib
import heapq
import json
import os
import sys

MANIFEST_NAME = "manifest.json"
MANIFEST_VERSION = 1

__all__ = [
    "ExternalSorter",
    "CorruptSegmentError",
    "ManifestError",
    "InputFormatError",
    "main",
]


class CorruptSegmentError(RuntimeError):
    """A temporary segment is missing, truncated, or fails validation."""


class ManifestError(RuntimeError):
    """The recovery manifest is unreadable or inconsistent."""


class InputFormatError(ValueError):
    """An input line is not a valid record."""


def _sha256_file(path: str) -> str:
    digest = hashlib.sha256()
    with open(path, "rb") as fh:
        for block in iter(lambda: fh.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def _fsync_dir(path: str) -> None:
    if not hasattr(os, "O_DIRECTORY"):
        return
    try:
        fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    except OSError:
        return
    try:
        os.fsync(fd)
    except OSError:
        pass
    finally:
        os.close(fd)


def _atomic_write_json(path: str, obj) -> None:
    tmp = f"{path}.tmp-{os.getpid()}"
    data = json.dumps(obj, indent=2).encode("utf-8")
    with open(tmp, "wb") as fh:
        fh.write(data)
        fh.flush()
        os.fsync(fh.fileno())
    os.replace(tmp, path)
    _fsync_dir(os.path.dirname(path) or ".")


class ExternalSorter:
    """Stable external merge sort with a record-counted memory budget."""

    def __init__(
        self,
        input_path: str,
        output_path: str,
        *,
        memory_records: int = 1000,
        fan_in: int = 8,
        work_dir: str | None = None,
        keep_work: bool = False,
        checkpoint_hook=None,
    ):
        if memory_records < 1:
            raise ValueError("memory_records must be >= 1")
        if fan_in < 2:
            raise ValueError("fan_in must be >= 2")
        self.input_path = os.path.abspath(input_path)
        self.output_path = os.path.abspath(output_path)
        self.memory_records = memory_records
        self.fan_in = fan_in
        self.work_dir = (
            os.path.abspath(work_dir)
            if work_dir is not None
            else self.output_path + ".extsort-work"
        )
        self.keep_work = keep_work
        # Test/observability hook invoked after every durable manifest commit.
        self.checkpoint_hook = checkpoint_hook
        self._manifest: dict | None = None
        self._key_kind: str | None = None

    @property
    def _manifest_path(self) -> str:
        return os.path.join(self.work_dir, MANIFEST_NAME)

    # ------------------------------------------------------------------ API

    def sort(self) -> str:
        """Run (or resume) the sort; returns the output path."""
        os.makedirs(self.work_dir, exist_ok=True)
        self._manifest = self._load_manifest()
        self._validate_segments()
        if self._manifest["total_records"] is None:
            self._generate_runs()
        self._merge_phase()
        self._final_merge()
        self._cleanup_work()
        return self.output_path

    # -------------------------------------------------------------- manifest

    def _load_manifest(self) -> dict:
        path = self._manifest_path
        if not os.path.exists(path):
            return {
                "version": MANIFEST_VERSION,
                "input": self.input_path,
                "output": self.output_path,
                "input_fingerprint": self._input_fingerprint(),
                "input_offset": 0,
                "total_records": None,
                "next_segment_id": 0,
                "segments": [],
            }
        try:
            with open(path, "r", encoding="utf-8") as fh:
                manifest = json.load(fh)
        except (OSError, json.JSONDecodeError) as exc:
            raise ManifestError(f"cannot read manifest {path}: {exc}") from exc
        if not isinstance(manifest, dict):
            raise ManifestError(f"manifest {path} is not a JSON object")
        required = {
            "version",
            "input",
            "output",
            "input_offset",
            "total_records",
            "next_segment_id",
            "segments",
        }
        missing = required - manifest.keys()
        if missing:
            raise ManifestError(f"manifest {path} missing fields: {sorted(missing)}")
        if manifest["version"] != MANIFEST_VERSION:
            raise ManifestError(
                f"manifest version {manifest['version']} != {MANIFEST_VERSION}"
            )
        if os.path.abspath(manifest["input"]) != self.input_path:
            raise ManifestError(
                f"manifest was created for input {manifest['input']!r}, "
                f"not {self.input_path!r}"
            )
        recorded = manifest.get("input_fingerprint")
        current = self._input_fingerprint()
        if recorded != current:
            raise ManifestError(
                f"input file {self.input_path!r} changed since the manifest "
                f"was created (fingerprint {recorded} != {current}); "
                f"remove the work directory to start over"
            )
        return manifest

    def _input_fingerprint(self):
        try:
            st = os.stat(self.input_path)
        except OSError:
            return None
        return [st.st_size, st.st_mtime_ns]

    def _commit_manifest(self, event: dict) -> None:
        _atomic_write_json(self._manifest_path, self._manifest)
        if self.checkpoint_hook is not None:
            self.checkpoint_hook(event)

    def _validate_segments(self) -> None:
        for seg in self._manifest["segments"]:
            path = os.path.join(self.work_dir, seg["path"])
            if not os.path.isfile(path):
                raise CorruptSegmentError(
                    f"segment listed in manifest is missing: {path} "
                    f"(refusing to drop it silently)"
                )
            digest = _sha256_file(path)
            if digest != seg["sha256"]:
                raise CorruptSegmentError(
                    f"segment checksum mismatch: {path} "
                    f"(refusing to drop it silently)"
                )

    # -------------------------------------------------------- run generation

    def _iter_input(self, skip: int):
        seen = 0
        with open(self.input_path, "r", encoding="utf-8") as fh:
            for lineno, line in enumerate(fh, 1):
                line = line.strip()
                if not line:
                    continue
                if seen < skip:
                    seen += 1
                    continue
                seen += 1
                try:
                    obj = json.loads(line)
                except json.JSONDecodeError as exc:
                    raise InputFormatError(
                        f"{self.input_path}:{lineno}: invalid JSON: {exc}"
                    ) from exc
                yield lineno, obj

    def _parse_input(self, obj, lineno: int):
        if not isinstance(obj, dict) or "key" not in obj:
            raise InputFormatError(
                f"line {lineno}: expected an object with 'key' and 'value'"
            )
        key = obj["key"]
        value = obj.get("value")
        if isinstance(key, bool) or not isinstance(key, (int, float, str)):
            raise InputFormatError(
                f"line {lineno}: key must be a string or a number, got {type(key).__name__}"
            )
        kind = "str" if isinstance(key, str) else "num"
        if self._key_kind is None:
            self._key_kind = kind
        elif self._key_kind != kind:
            raise InputFormatError(
                f"line {lineno}: cannot mix string and numeric keys"
            )
        return key, value

    def _generate_runs(self) -> None:
        manifest = self._manifest
        skip = manifest["input_offset"]
        seq = skip
        pending: list[tuple] = []

        def flush() -> None:
            nonlocal pending
            if not pending:
                return
            pending.sort(key=lambda rec: (rec[0], rec[1]))
            segment = self._write_segment(pending)
            manifest["segments"].append(segment)
            manifest["next_segment_id"] += 1
            manifest["input_offset"] = seq
            self._commit_manifest({"event": "run_written", "segment": segment["path"]})
            pending = []

        for lineno, obj in self._iter_input(skip):
            key, value = self._parse_input(obj, lineno)
            pending.append((key, seq, value))
            seq += 1
            if len(pending) >= self.memory_records:
                flush()
        flush()
        manifest["total_records"] = seq
        self._commit_manifest({"event": "input_complete", "total_records": seq})

    def _write_segment(self, records) -> dict:
        seg_id = self._manifest["next_segment_id"]
        name = f"run-{seg_id:06d}.seg"
        path = os.path.join(self.work_dir, name)
        tmp = f"{path}.tmp-{os.getpid()}"
        digest = hashlib.sha256()
        count = 0
        with open(tmp, "wb") as fh:
            for key, seq, value in records:
                line = (
                    json.dumps([key, seq, value], ensure_ascii=False, separators=(",", ":"))
                    + "\n"
                ).encode("utf-8")
                digest.update(line)
                fh.write(line)
                count += 1
            fh.flush()
            os.fsync(fh.fileno())
        os.replace(tmp, path)
        _fsync_dir(self.work_dir)
        return {"path": name, "records": count, "sha256": digest.hexdigest()}

    # ----------------------------------------------------------------- merge

    def _segment_cursor(self, seg: dict):
        """Streaming cursor over one segment; validates as it reads."""
        path = os.path.join(self.work_dir, seg["path"])
        count = 0
        try:
            fh = open(path, "r", encoding="utf-8")
        except FileNotFoundError as exc:
            raise CorruptSegmentError(f"missing segment file: {path}") from exc
        with fh:
            for lineno, line in enumerate(fh, 1):
                line = line.strip()
                if not line:
                    continue
                try:
                    item = json.loads(line)
                    key, seq, value = item
                except (json.JSONDecodeError, ValueError, TypeError) as exc:
                    raise CorruptSegmentError(
                        f"{path}:{lineno}: invalid record: {exc}"
                    ) from exc
                count += 1
                yield (key, seq, value)
        if count != seg["records"]:
            raise CorruptSegmentError(
                f"{path}: expected {seg['records']} records, found {count}"
            )

    def _merge_cursor(self, segments):
        """k-way streaming merge cursor; holds no more than fan_in records."""
        return heapq.merge(
            *(self._segment_cursor(seg) for seg in segments),
            key=lambda rec: (rec[0], rec[1]),
        )

    def _merge_segments_to_file(self, group) -> dict:
        expected = sum(seg["records"] for seg in group)
        seg_id = self._manifest["next_segment_id"]
        name = f"run-{seg_id:06d}.seg"
        path = os.path.join(self.work_dir, name)
        tmp = f"{path}.tmp-{os.getpid()}"
        digest = hashlib.sha256()
        count = 0
        with open(tmp, "wb") as fh:
            for rec in self._merge_cursor(group):
                line = (
                    json.dumps(list(rec), ensure_ascii=False, separators=(",", ":"))
                    + "\n"
                ).encode("utf-8")
                digest.update(line)
                fh.write(line)
                count += 1
            fh.flush()
            os.fsync(fh.fileno())
        os.replace(tmp, path)
        _fsync_dir(self.work_dir)
        if count != expected:
            raise CorruptSegmentError(
                f"merge of {[s['path'] for s in group]} produced {count} "
                f"records, expected {expected}"
            )
        return {"path": name, "records": count, "sha256": digest.hexdigest()}

    def _merge_phase(self) -> None:
        manifest = self._manifest
        while len(manifest["segments"]) > self.fan_in:
            group = manifest["segments"][: self.fan_in]
            merged = self._merge_segments_to_file(group)
            manifest["segments"] = manifest["segments"][self.fan_in :] + [merged]
            manifest["next_segment_id"] += 1
            self._commit_manifest({"event": "merge_round", "segment": merged["path"]})
            # Files are removed only after the manifest no longer references
            # them, so a crash here is still recoverable.
            for seg in group:
                self._remove_segment_file(seg)

    def _remove_segment_file(self, seg: dict) -> None:
        try:
            os.remove(os.path.join(self.work_dir, seg["path"]))
        except FileNotFoundError:
            pass

    # ----------------------------------------------------------------- final

    def _final_merge(self) -> None:
        manifest = self._manifest
        expected = manifest["total_records"]
        out_dir = os.path.dirname(self.output_path) or "."
        os.makedirs(out_dir, exist_ok=True)
        tmp = f"{self.output_path}.tmp-{os.getpid()}"
        count = 0
        with open(tmp, "wb") as fh:
            for key, seq, value in self._merge_cursor(manifest["segments"]):
                line = (
                    json.dumps({"key": key, "seq": seq, "value": value}, ensure_ascii=False)
                    + "\n"
                ).encode("utf-8")
                fh.write(line)
                count += 1
            fh.flush()
            os.fsync(fh.fileno())
        if count != expected:
            os.unlink(tmp)
            raise CorruptSegmentError(
                f"final output would contain {count} records, expected {expected}; "
                f"refusing to publish partial output"
            )
        os.replace(tmp, self.output_path)
        _fsync_dir(out_dir)

    # --------------------------------------------------------------- cleanup

    def _cleanup_work(self) -> None:
        if self.keep_work:
            return
        try:
            names = os.listdir(self.work_dir)
        except OSError:
            return
        for name in names:
            if name == MANIFEST_NAME or name.startswith("run-"):
                try:
                    os.remove(os.path.join(self.work_dir, name))
                except OSError:
                    pass
        try:
            os.rmdir(self.work_dir)
        except OSError:
            pass


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        prog="extsort",
        description=(
            "Finite-memory external merge sort. Stable: equal keys keep their "
            "original input order. Resumable after interruption via a "
            "checksummed manifest."
        ),
    )
    parser.add_argument(
        "input",
        help='input JSONL file: one {"key": ..., "value": ...} object per line',
    )
    parser.add_argument("output", help="output JSONL file (atomically replaced)")
    parser.add_argument(
        "--memory-records",
        type=int,
        default=1000,
        help="max records held in memory at once (default: 1000)",
    )
    parser.add_argument(
        "--fan-in",
        type=int,
        default=8,
        help="max merge fan-in, i.e. open segment files (default: 8)",
    )
    parser.add_argument(
        "--work-dir",
        default=None,
        help="directory for temp segments and the manifest "
        "(default: <output>.extsort-work)",
    )
    parser.add_argument(
        "--keep-work",
        action="store_true",
        help="keep the work directory after a successful sort",
    )
    args = parser.parse_args(argv)

    sorter = ExternalSorter(
        args.input,
        args.output,
        memory_records=args.memory_records,
        fan_in=args.fan_in,
        work_dir=args.work_dir,
        keep_work=args.keep_work,
    )
    try:
        sorter.sort()
    except (CorruptSegmentError, ManifestError, InputFormatError) as exc:
        print(f"extsort: error: {exc}", file=sys.stderr)
        return 2
    except OSError as exc:
        print(f"extsort: error: {exc}", file=sys.stderr)
        return 1
    print(f"extsort: wrote {args.output}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
