"""Durable write-ahead log and checkpoint storage.

Layout inside the data directory:
    wal.log            newline-delimited JSON transaction records
    checkpoint.json    full snapshot (state + integrity checksum)
    checkpoint.json.tmp  temporary file used for atomic checkpoint replace

Publish point: a transaction becomes visible to readers only after its
log record has been written *and* fsynced (``log:after_fsync``).

Crash-injection points (env SR_FAULT_AT, comma separated) simulate power
loss via os._exit() at every persistence boundary:
    log:before_write, log:partial_write, log:after_write_before_fsync,
    log:after_fsync,
    ckpt:before_tmp_write, ckpt:after_tmp_write, ckpt:after_rename,
    ckpt:before_log_rotate, ckpt:after_log_rotate
SR_FAULT_SKIP=n skips the first n fault triggers so a later boundary can
be targeted.
"""

from __future__ import annotations

import hashlib
import json
import os

from .errors import CorruptionError

FAULT_ENV = "SR_FAULT_AT"
FAULT_SKIP_ENV = "SR_FAULT_SKIP"

_FAULTS = set(filter(None, os.environ.get(FAULT_ENV, "").split(",")))
_fault_skip = int(os.environ.get(FAULT_SKIP_ENV, "0") or "0")


def fault(point):
    """Crash-injection hook: terminate the process at the named boundary."""
    global _fault_skip
    if point in _FAULTS:
        if _fault_skip > 0:
            _fault_skip -= 1
            return
        os._exit(1)


def _canonical(obj):
    return json.dumps(obj, sort_keys=True, separators=(",", ":"))


def _sha(obj):
    return hashlib.sha256(_canonical(obj).encode("utf-8")).hexdigest()


def make_txn_record(txn_id, version, ops_wire, opsha):
    rec = {"txn_id": txn_id, "version": version, "ops": ops_wire, "opsha": opsha}
    rec["sha"] = _sha(rec)
    return rec


def verify_txn_record(rec):
    if not isinstance(rec, dict):
        raise ValueError("record is not an object")
    body = {k: rec[k] for k in ("txn_id", "version", "ops", "opsha")}
    if _sha(body) != rec.get("sha"):
        raise ValueError("checksum mismatch")
    return rec


class Storage:
    def __init__(self, path):
        self.path = path
        os.makedirs(path, exist_ok=True)
        self.log_path = os.path.join(path, "wal.log")
        self.ckpt_path = os.path.join(path, "checkpoint.json")
        self.ckpt_tmp = os.path.join(path, "checkpoint.json.tmp")
        self._log = None

    # -- lifecycle ------------------------------------------------------
    def has_checkpoint(self):
        return os.path.exists(self.ckpt_path)

    def open_append(self):
        self._log = open(self.log_path, "ab")

    def close(self):
        if self._log is not None:
            self._log.close()
            self._log = None

    def _fsync_dir(self):
        fd = os.open(self.path, os.O_RDONLY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)

    # -- write-ahead log ------------------------------------------------
    def append_txn(self, record):
        """Durably append one transaction record (the publish point)."""
        line = (_canonical(record) + "\n").encode("utf-8")
        fault("log:before_write")
        if "log:partial_write" in _FAULTS:
            # Torn write: only part of the record reaches stable storage.
            self._log.write(line[: max(1, len(line) // 2)])
            self._log.flush()
            os.fsync(self._log.fileno())
            os._exit(1)
        self._log.write(line)
        fault("log:after_write_before_fsync")
        self._log.flush()
        os.fsync(self._log.fileno())
        fault("log:after_fsync")

    def recover(self):
        """Read valid committed records; truncate a torn tail.

        A trailing partial/corrupt record (torn write) is ignored and the
        file is truncated back to the last valid record boundary so future
        appends stay well-formed.  Corruption anywhere before the tail is
        fatal.
        """
        records = []
        if not os.path.exists(self.log_path):
            return records
        with open(self.log_path, "rb") as f:
            data = f.read()
        valid_len = 0
        lines = data.split(b"\n")
        for idx, raw in enumerate(lines):
            is_last = idx == len(lines) - 1
            if not raw:
                if is_last:
                    break
                raise CorruptionError(f"wal: empty record at line {idx}")
            try:
                rec = verify_txn_record(json.loads(raw))
            except Exception as exc:
                if is_last:
                    break  # truncated tail from a torn write
                raise CorruptionError(f"wal: corrupt record at line {idx}: {exc}")
            records.append(rec)
            valid_len += len(raw) + 1
        if valid_len < len(data):
            with open(self.log_path, "r+b") as f:
                f.truncate(valid_len)
        return records

    # -- checkpoint ------------------------------------------------------
    def write_checkpoint(self, state):
        """Atomically install a checkpoint, then rotate the log."""
        blob = _canonical({"sha": _sha(state), "state": state}).encode("utf-8")
        fault("ckpt:before_tmp_write")
        with open(self.ckpt_tmp, "wb") as f:
            f.write(blob)
            f.flush()
            os.fsync(f.fileno())
        fault("ckpt:after_tmp_write")
        os.replace(self.ckpt_tmp, self.ckpt_path)
        self._fsync_dir()
        fault("ckpt:after_rename")
        # Log rotation: everything up to the checkpoint version is now
        # captured by the checkpoint, so the log can start over empty.
        fault("ckpt:before_log_rotate")
        self.close()
        with open(self.log_path, "wb") as f:
            f.flush()
            os.fsync(f.fileno())
        self._fsync_dir()
        fault("ckpt:after_log_rotate")
        self.open_append()

    def read_checkpoint(self):
        if not os.path.exists(self.ckpt_path):
            return None
        with open(self.ckpt_path, "rb") as f:
            blob = f.read()
        try:
            obj = json.loads(blob)
            state = obj["state"]
            if _sha(state) != obj["sha"]:
                raise ValueError("checksum mismatch")
        except Exception as exc:
            raise CorruptionError(f"checkpoint corrupt: {exc}")
        return state
