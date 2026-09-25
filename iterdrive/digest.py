"""Canonical input serialization and content digest.

The digest binds a checkpoint to the exact problem instance (matrix, rhs,
size) and the iterator algorithm. Any change to the inputs produces a
different digest, so stale checkpoints are rejected on resume.
"""

import hashlib
import json


def canonical_json(obj):
    """Deterministic JSON serialization (sorted keys, tight separators)."""
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), allow_nan=False)


def input_digest(problem, iterator_name):
    """SHA-256 over the canonical form of {problem, iterator}."""
    blob = canonical_json({"iterator": iterator_name, "problem": problem})
    return hashlib.sha256(blob.encode("utf-8")).hexdigest()
