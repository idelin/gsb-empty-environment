"""Checkpoint persistence: atomic save, integrity-checked load.

File layout: a JSON object {"checksum": ..., "envelope": {...}} where the
checksum is SHA-256 over the canonical serialization of the envelope.
Writes go to a temporary file which is fsynced and then atomically
renamed over the target, so an interrupted save can never leave a
partially written checkpoint at the target path.
"""

import hashlib
import json
import os
from pathlib import Path

from .digest import canonical_json

SCHEMA_VERSION = 1


class CheckpointError(Exception):
    """Base class for checkpoint failures."""


class CheckpointCorrupt(CheckpointError):
    """The checkpoint file is unreadable, malformed, or tampered with."""


class InputMismatch(CheckpointError):
    """The checkpoint belongs to different inputs (matrix/rhs/iterator)."""


class CheckpointStore:
    def __init__(self, path):
        self.path = Path(path)
        self.tmp_path = self.path.with_name(self.path.name + ".tmp")

    def save(self, payload):
        envelope = {"schema": SCHEMA_VERSION, "payload": payload}
        checksum = hashlib.sha256(
            canonical_json(envelope).encode("utf-8")
        ).hexdigest()
        blob = canonical_json({"checksum": checksum, "envelope": envelope})
        with open(self.tmp_path, "w", encoding="utf-8") as fh:
            fh.write(blob)
            fh.flush()
            os.fsync(fh.fileno())
        os.replace(self.tmp_path, self.path)

    def load(self):
        """Return the payload dict, or None if no checkpoint exists.

        Raises CheckpointCorrupt if the file exists but fails integrity
        checks (bad JSON, missing fields, schema mismatch, bad checksum).
        Leftover ``*.tmp`` files from interrupted saves are ignored.
        """
        if not self.path.exists():
            return None
        try:
            blob = json.loads(self.path.read_text(encoding="utf-8"))
            envelope = blob["envelope"]
            checksum = blob["checksum"]
        except (json.JSONDecodeError, KeyError, TypeError, UnicodeDecodeError) as exc:
            raise CheckpointCorrupt(f"unparseable checkpoint: {exc}") from exc
        if not isinstance(envelope, dict) or envelope.get("schema") != SCHEMA_VERSION:
            raise CheckpointCorrupt("unsupported or missing schema version")
        expect = hashlib.sha256(
            canonical_json(envelope).encode("utf-8")
        ).hexdigest()
        if not isinstance(checksum, str) or checksum != expect:
            raise CheckpointCorrupt("checksum mismatch (tampered or truncated)")
        return envelope["payload"]
