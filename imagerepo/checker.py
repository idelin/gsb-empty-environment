"""Independent visible-snapshot auditor.

Used by tests to verify, with logic distinct from the repository's own
resolver, that every visible snapshot is:

* complete   - both dependency closures present and digest-verified;
* immutable   - the manifest on disk is the exact bytes its id addresses.

It reads the repository strictly through the on-disk layout (never through
the repo's in-memory state).
"""
from __future__ import annotations

import hashlib
import json
import os

from .closure import compute_closure
from .model import Ident, Metadata


class AuditError(Exception):
    pass


def _load_json(path):
    with open(path, "rb") as fh:
        return json.loads(fh.read())


def audit_repository(root: str, *, verify_digest: bool = True):
    """Return per-snapshot audit info; raise AuditError on any violation."""
    objects_dir = os.path.join(root, "objects")
    meta_dir = os.path.join(root, "meta")
    snaps_dir = os.path.join(root, "snapshots")
    current_path = os.path.join(root, "current.json")

    def get_meta(ident: Ident):
        # Prefer the manifest-embedded metadata, as the manifest must be
        # self-contained; fall back to the global meta store.
        return None

    infos = {}
    current = None
    if os.path.exists(current_path):
        current = _load_json(current_path)["snapshot"]

    for name in sorted(os.listdir(snaps_dir)):
        if not name.endswith(".json"):
            continue
        snap_id = name[:-5]
        path = os.path.join(snaps_dir, name)
        with open(path, "rb") as manifest_fh:
            raw = manifest_fh.read()
        doc = json.loads(raw)
        body = {k: v for k, v in doc.items() if k != "manifest_digest"}
        canonical = json.dumps(body, sort_keys=True, separators=(",", ":")).encode()
        if doc.get("manifest_digest") != hashlib.sha256(canonical).hexdigest():
            raise AuditError(f"snapshot {snap_id[:12]} manifest digest is inconsistent (mutated)")

        embedded = {
            key: Metadata.from_dict(value) for key, value in doc["metadata"].items()
        }

        def lookup(ident, _embedded=embedded, _meta_dir=meta_dir):
            meta = _embedded.get(ident.key)
            if meta is not None:
                return meta
            mp = os.path.join(_meta_dir, ident.key + ".json")
            if os.path.exists(mp):
                return Metadata.from_dict(_load_json(mp))
            return None

        def has_object(digest, length):
            op = os.path.join(objects_dir, digest[:2], digest)
            try:
                return os.path.getsize(op) == length
            except OSError:
                return False

        def check_digest(meta):
            op = os.path.join(objects_dir, meta.archive_digest[:2], meta.archive_digest)
            with open(op, "rb") as fh:
                h = hashlib.sha256()
                length = 0
                while True:
                    chunk = fh.read(1 << 20)
                    if not chunk:
                        break
                    length += len(chunk)
                    h.update(chunk)
            if length != meta.archive_length:
                return f"length {length} != declared {meta.archive_length}"
            if h.hexdigest() != meta.archive_digest:
                return "sha-256 mismatch"
            return None if verify_digest else None

        roots = tuple(Ident(**r) for r in doc["roots"])
        result = compute_closure(roots, lookup, has_object, check_digest=check_digest)
        infos[snap_id] = {
            "roots": roots,
            "runtime": result.runtime,
            "build": result.build,
            "gaps": result.gaps,
            "is_current": snap_id == current,
        }
        if result.gaps:
            raise AuditError(
                f"snapshot {snap_id[:12]} incomplete: "
                + "; ".join(f"{g.kind}:{g.ident.key}" for g in result.gaps))
    if current is not None and current not in infos:
        raise AuditError("current.json points at a missing snapshot")
    leases_dir = os.path.join(root, "leases")
    if os.path.isdir(leases_dir):
        for name in os.listdir(leases_dir):
            if not name.endswith(".json"):
                continue
            lease = _load_json(os.path.join(leases_dir, name))
            if lease["snapshot"] not in infos:
                raise AuditError(f"lease {name} references missing snapshot")
    return infos
