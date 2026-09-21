"""Canonical JSON serialization and SHA-256 helpers."""

from __future__ import annotations

import hashlib
import json
from typing import Any, Mapping, Sequence


def canonical_json(value: Any) -> bytes:
    """Serialize *value* deterministically: sorted keys, compact separators."""
    return json.dumps(
        value,
        sort_keys=True,
        separators=(",", ":"),
        ensure_ascii=False,
    ).encode("utf-8")


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def short(digest: str, length: int = 12) -> str:
    return digest[:length]


def materialize_inputs(inputs: Any) -> Any:
    """Normalize a mapping/sequence/scalar into JSON-comparable material.

    Keys are sorted recursively so that insertion order in the manifest can
    never change the build key.
    """
    if isinstance(inputs, Mapping):
        return {str(key): materialize_inputs(inputs[key]) for key in sorted(inputs, key=str)}
    if isinstance(inputs, (list, tuple)) or (
        isinstance(inputs, Sequence) and not isinstance(inputs, (str, bytes, bytearray))
    ):
        return [materialize_inputs(item) for item in inputs]
    if isinstance(inputs, (bytes, bytearray)):
        return {"__bytes_sha256__": sha256_bytes(bytes(inputs))}
    if inputs is None or isinstance(inputs, (str, bool, int, float)):
        return inputs
    # Fall back to canonical JSON for exotic scalar types; raise if impossible.
    return json.loads(json.dumps(inputs, sort_keys=True))


def build_key(package: str, version: str, inputs: Any, outputs: Sequence[str]) -> str:
    """Derive the cache key for a build node.

    The node name is intentionally excluded: two nodes producing the same
    package/version/inputs/output set are the same build. Declared outputs are
    part of the key because they define the node's contract.
    """
    payload = {
        "package": package,
        "version": version,
        "inputs": materialize_inputs(inputs),
        "outputs": sorted(outputs),
    }
    return sha256_bytes(canonical_json(payload))


def input_digest(inputs: Any) -> str:
    return sha256_bytes(canonical_json(materialize_inputs(inputs)))
