"""Checkpoint model, input digest, and an atomic file store.

Checkpoint file layout (JSON)::

    {"sha256": <hex of canonical payload JSON>, "payload": {...}}

The payload carries everything needed to resume exactly: the input
digest, the solver kind, the completed iteration count, the opaque
solver state, the residual history, and the callback ledger (the
"evidence" of confirmed external side effects).

Saves are atomic: the payload is written to a temporary file in the
same directory, fsynced, then ``os.replace``d onto the target path.
An interrupted save therefore never destroys the previous checkpoint;
a leftover ``.ckpt-*.tmp`` file is simply ignored on load.
"""
from __future__ import annotations

import hashlib
import json
import os
import struct
import tempfile
from dataclasses import dataclass, field

from .dense import check_matrix, check_vector

FORMAT_VERSION = 1


class CheckpointError(Exception):
    """Base class for checkpoint failures."""


class CheckpointCorruptError(CheckpointError):
    """Checkpoint file is unreadable or fails its integrity check."""


class CheckpointInputMismatch(CheckpointError):
    """Checkpoint was produced for different inputs; resume refused."""


def input_digest(matrix, n_rows, n_cols, rhs, n, params=None):
    """Content digest of the problem inputs.

    Any change to the matrix, the right-hand side, the declared sizes
    or the solver parameters yields a different digest, which makes
    stale checkpoints detectable.
    """
    check_matrix(matrix, n_rows, n_cols)
    check_vector(rhs, n)
    h = hashlib.sha256()
    h.update(b"iterdriver/v1\n")
    h.update(struct.pack("<QQ", n_rows, n_cols))
    for i in range(n_rows):
        for j in range(n_cols):
            h.update(struct.pack("<d", matrix[i][j]))
    h.update(struct.pack("<Q", n))
    for i in range(n):
        h.update(struct.pack("<d", rhs[i]))
    h.update(json.dumps(params or {}, sort_keys=True).encode("utf-8"))
    return h.hexdigest()


@dataclass
class Checkpoint:
    input_digest: str
    solver_kind: str
    iteration: int
    solver_state: dict
    residual_history: list
    callback_ledger: dict = field(default_factory=dict)
    format_version: int = FORMAT_VERSION

    def to_dict(self):
        return {
            "format_version": self.format_version,
            "input_digest": self.input_digest,
            "solver_kind": self.solver_kind,
            "iteration": self.iteration,
            "solver_state": self.solver_state,
            "residual_history": list(self.residual_history),
            "callback_ledger": {k: dict(v)
                                for k, v in self.callback_ledger.items()},
        }

    @classmethod
    def from_dict(cls, payload):
        try:
            version = payload["format_version"]
            if version != FORMAT_VERSION:
                raise CheckpointCorruptError(
                    f"unsupported checkpoint format version {version!r}")
            cp = cls(
                input_digest=str(payload["input_digest"]),
                solver_kind=str(payload["solver_kind"]),
                iteration=int(payload["iteration"]),
                solver_state=dict(payload["solver_state"]),
                residual_history=[float(r)
                                  for r in payload["residual_history"]],
                callback_ledger={str(k): dict(v) for k, v in
                                 payload["callback_ledger"].items()},
            )
        except CheckpointCorruptError:
            raise
        except (KeyError, TypeError, ValueError) as exc:
            raise CheckpointCorruptError(
                f"malformed checkpoint payload: {exc}") from exc
        if cp.iteration != len(cp.residual_history):
            raise CheckpointCorruptError(
                "iteration count does not match residual history length")
        return cp


def _canonical(payload_dict):
    return json.dumps(payload_dict, sort_keys=True).encode("utf-8")


def _encode(checkpoint):
    payload = checkpoint.to_dict()
    blob = {"sha256": hashlib.sha256(_canonical(payload)).hexdigest(),
            "payload": payload}
    return json.dumps(blob).encode("utf-8")


def _decode(raw):
    try:
        blob = json.loads(raw)
        payload = blob["payload"]
        recorded = blob["sha256"]
    except (json.JSONDecodeError, KeyError, TypeError) as exc:
        raise CheckpointCorruptError(
            f"unreadable checkpoint envelope: {exc}") from exc
    actual = hashlib.sha256(_canonical(payload)).hexdigest()
    if actual != recorded:
        raise CheckpointCorruptError("checkpoint integrity check failed")
    return Checkpoint.from_dict(payload)


class FileCheckpointStore:
    """Atomic JSON checkpoint file.

    ``_write_all`` is the single seam used by tests to inject an
    interrupted save.
    """

    def __init__(self, path):
        self.path = path

    def exists(self):
        return os.path.exists(self.path)

    def save(self, checkpoint):
        data = _encode(checkpoint)
        directory = os.path.dirname(os.path.abspath(self.path))
        fd, tmp_path = tempfile.mkstemp(dir=directory,
                                        prefix=".ckpt-", suffix=".tmp")
        try:
            with os.fdopen(fd, "wb") as fh:
                self._write_all(fh, data)
                fh.flush()
                os.fsync(fh.fileno())
            os.replace(tmp_path, self.path)
        except BaseException:
            # Interrupted save: drop the partial temp file; the previous
            # checkpoint (if any) is untouched.
            try:
                os.unlink(tmp_path)
            except OSError:
                pass
            raise

    def _write_all(self, fh, data):
        fh.write(data)

    def load(self):
        try:
            with open(self.path, "rb") as fh:
                raw = fh.read()
        except FileNotFoundError:
            raise CheckpointError(
                f"no checkpoint at {self.path}") from None
        return _decode(raw)
