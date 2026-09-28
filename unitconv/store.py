"""JSON persistence for the versioned unit-conversion graph.

A store file keeps the raw statement strings of every committed batch;
loading means replaying the log through :class:`VersionedGraph`, which
guarantees every stored rule is re-validated exactly.  Writes are atomic
(write-temp-then-``os.replace``), and rejected batches never reach disk.
"""

from __future__ import annotations

import json
import os
import tempfile
from datetime import datetime, timezone
from typing import List, Optional, Sequence, Tuple

from .core import CommitInfo, VersionedGraph

STORE_FORMAT = "unitconv-store-v1"
DEFAULT_STORE_PATH = "unitconv_store.json"


def utc_now_iso() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def init_store(path: str) -> None:
    """Create an empty store file; fail if one already exists."""
    if os.path.exists(path):
        raise FileExistsError(f"store already exists: {path}")
    _write_atomic(path, {"format": STORE_FORMAT, "batches": []})


def _write_atomic(path: str, payload: dict) -> None:
    directory = os.path.dirname(os.path.abspath(path))
    os.makedirs(directory, exist_ok=True)
    fd, tmp_path = tempfile.mkstemp(prefix=".unitconv-", suffix=".tmp",
                                    dir=directory)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            json.dump(payload, handle, ensure_ascii=False, indent=2,
                      sort_keys=False)
            handle.write("\n")
        os.replace(tmp_path, path)
    except BaseException:
        try:
            os.unlink(tmp_path)
        except OSError:
            pass
        raise


def _read(path: str) -> dict:
    with open(path, "r", encoding="utf-8") as handle:
        payload = json.load(handle)
    if not isinstance(payload, dict) or payload.get("format") != STORE_FORMAT:
        raise ValueError(f"not a {STORE_FORMAT} file: {path}")
    if not isinstance(payload.get("batches"), list):
        raise ValueError(f"corrupt store (missing batches list): {path}")
    return payload


def load_store(path: str) -> VersionedGraph:
    """Replay the store log into a fresh :class:`VersionedGraph`."""
    payload = _read(path)
    graph = VersionedGraph()
    for index, entry in enumerate(payload["batches"], start=1):
        if not isinstance(entry, dict):
            raise ValueError(f"corrupt store: batch {index} is not an object")
        statements = entry.get("statements")
        if not isinstance(statements, list) or not all(
                isinstance(item, str) for item in statements):
            raise ValueError(f"corrupt store: batch {index} statements invalid")
        timestamp = entry.get("timestamp")
        info = graph.batch(statements, timestamp=timestamp)
        stored_version = entry.get("version")
        if stored_version is not None and stored_version != info.version:
            raise ValueError(
                f"corrupt store: batch {index} claims version "
                f"{stored_version}, replay produced {info.version}")
    return graph


def append_batch(path: str, statements: Sequence[str],
                 timestamp: Optional[str] = None
                 ) -> Tuple[VersionedGraph, CommitInfo]:
    """Validate-and-commit one batch on disk; rejected batches write nothing.

    The file is loaded, the batch applied in memory (raising on failure),
    and only then is the whole log rewritten atomically.
    """
    if not os.path.exists(path):
        raise FileNotFoundError(
            f"store not found: {path} (run 'init' first)")
    graph = load_store(path)
    info = graph.batch(statements, timestamp=timestamp or utc_now_iso())
    payload = _read(path)
    payload["batches"].append({
        "version": info.version,
        "timestamp": info.timestamp,
        "statements": list(statements),
    })
    _write_atomic(path, payload)
    return graph, info


def stored_batches(path: str) -> List[dict]:
    return list(_read(path)["batches"])
