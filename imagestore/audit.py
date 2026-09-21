"""Independent, read-only repository auditor.

This module intentionally re-implements traversal and verification from
scratch rather than importing :mod:`imagestore.repo` helpers: it reads the
on-disk directory format directly with its own SHA-256 checks, its own
least-fixed-point closure calculator and its own missing-item classifier.
Its purpose is to serve as an independent oracle in the tests.

Checks per *visible* snapshot (current snapshot plus snapshots referenced by
live lease files):

* every declared package has a readable metadata object and archive object;
* both objects hash to their declared digests (manifest immutable content);
* the manifest on disk is byte-identical to the payload embedded in the
  snapshot-publish journal record (commit immutability);
* the two dependency closures are complete;
* every transitive coordinate is contained in the snapshot package list.
"""

import hashlib
import json
import os
from collections import deque

META_MISSING = "metadata-missing"
CONTENT_MISSING = "content-missing"
DIGEST_MISMATCH = "digest-mismatch"


def _key(coord):
    return (coord["name"], coord["version"], coord["platform"])


def _sha256_file(path):
    hasher = hashlib.sha256()
    if not os.path.isfile(path):
        return None
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 16), b""):
            hasher.update(chunk)
    return hasher.hexdigest()


def _object_path(root, digest):
    return os.path.join(root, "objects", digest[:2], digest[2:])


def _fixed_point(roots, meta_by_key, labels):
    """Independent BFS least fixed point with root-relative paths."""
    seen = {_key(r): [r] for r in roots}
    queue = deque(list(roots))
    missing = {}
    while queue:
        node = queue.popleft()
        meta = meta_by_key.get(_key(node))
        if meta is None:
            missing.setdefault(_key(node), seen[_key(node)])
            continue
        for label in labels:
            for dep in meta.get(label, ()):
                if _key(dep) not in seen:
                    seen[_key(dep)] = seen[_key(node)] + [label, dep]
                    queue.append(dep)
    return seen, missing


def _two_closures(root, meta_by_key):
    runtime, runtime_missing = _fixed_point([root], meta_by_key, ("run_requires",))
    union, union_missing = _fixed_point(
        [root], meta_by_key, ("run_requires", "host_requires")
    )
    host_seeds = set()
    for key in runtime:
        meta = meta_by_key.get(key)
        if meta is not None:
            for tool in meta.get("host_requires", ()):
                host_seeds.add(_key(tool))
    return runtime, host_seeds, union, union_missing


def _read_records(root):
    """Minimal independent journal parser (validates frames like journal.py)."""
    path = os.path.join(root, "journal")
    records = []
    if not os.path.isfile(path):
        return records
    with open(path, "rb") as journal_handle:
        data = journal_handle.read()
    offset = 0
    while offset < len(data):
        magic = data[offset:offset + 4]
        if magic != b"IMJ1" or len(data) - offset < 12:
            break
        length = int.from_bytes(data[offset + 4:offset + 12], "big")
        end = offset + 12 + length + 32
        if end > len(data):
            break
        frame = data[offset:end]
        if hashlib.sha256(frame[:12 + length]).digest() != frame[12 + length:]:
            raise AssertionError("auditor: journal checksum failure")
        records.append(json.loads(frame[12:12 + length]))
        offset = end
    return records


def _catalog_from_disk(root):
    path = os.path.join(root, "catalog.json")
    if not os.path.isfile(path):
        return {}
    raw = json.loads(open(path, "rb").read())
    if isinstance(raw, dict):
        return raw
    return {
        (e["name"], e["version"], e["platform"]): e for e in raw
    }


class AuditResult:
    def __init__(self):
        self.ok = True
        self.visible_snapshots = []
        self.errors = []
        self.snapshots = {}

    def fail(self, snapshot_id, message):
        self.ok = False
        self.errors.append(f"{snapshot_id}: {message}")


def visible_snapshot_ids(root):
    ids = set()
    current_path = os.path.join(root, "current")
    if os.path.isfile(current_path):
        with open(current_path, "r", encoding="utf-8") as handle:
            snap = handle.read().strip()
        if snap:
            ids.add(snap)
    lease_dir = os.path.join(root, "leases")
    if os.path.isdir(lease_dir):
        for name in os.listdir(lease_dir):
            if not name.endswith(".json"):
                continue
            with open(os.path.join(lease_dir, name), "rb") as lease_handle:
                lease = json.loads(lease_handle.read())
            ids.add(lease["snapshot"])
    return ids


def audit(root):
    """Audit every visible snapshot; return an :class:`AuditResult`."""
    result = AuditResult()
    snap_dir = os.path.join(root, "snapshots")
    published = {}
    manifest_bytes_by_id = {}
    for record in _read_records(root):
        if record.get("type") == "snapshot-publish":
            sid = record["snapshot"]
            manifest_bytes_by_id[sid] = record["manifest"].encode("utf-8")
            published[sid] = json.loads(record["manifest"])

    disk_manifests = {}
    if os.path.isdir(snap_dir):
        for name in os.listdir(snap_dir):
            if name.endswith(".json"):
                disk_manifests[name[:-5]] = os.path.join(snap_dir, name)

    for sid in sorted(visible_snapshot_ids(root)):
        result.visible_snapshots.append(sid)
        if sid not in published:
            result.fail(sid, "visible snapshot missing from journal")
            continue
        manifest = published[sid]
        result.snapshots[sid] = manifest

        # Journal payload vs on-disk manifest: immutability.
        disk_path = disk_manifests.get(sid)
        if disk_path is None:
            result.fail(sid, "manifest file missing from snapshots/")
        else:
            with open(disk_path, "rb") as manifest_handle:
                on_disk = manifest_handle.read()
            canonical = json.dumps(
                manifest, sort_keys=True, separators=(",", ":")
            ).encode("utf-8")
            if on_disk not in (canonical, manifest_bytes_by_id[sid]):
                result.fail(sid, "on-disk manifest differs from committed record")

        packages = {_key(p): p for p in manifest["packages"]}
        meta_by_key = {}
        for key, package in packages.items():
            meta_digest = package["meta_digest"]
            arch_digest = package["archive_digest"]
            meta_actual = _sha256_file(_object_path(root, meta_digest))
            if meta_actual is None:
                result.fail(sid, f"{_fmt(key)} {CONTENT_MISSING}: metadata object")
                continue
            if meta_actual != meta_digest:
                result.fail(sid, f"{_fmt(key)} {DIGEST_MISMATCH}: metadata object")
                continue
            arch_actual = _sha256_file(_object_path(root, arch_digest))
            if arch_actual is None:
                result.fail(sid, f"{_fmt(key)} {CONTENT_MISSING}: archive object")
                continue
            if arch_actual != arch_digest:
                result.fail(sid, f"{_fmt(key)} {DIGEST_MISMATCH}: archive object")
                continue
            with open(_object_path(root, meta_digest), "rb") as meta_handle:
                meta_by_key[key] = json.loads(meta_handle.read())

        root_coord = manifest["root"]
        if _key(root_coord) not in packages:
            result.fail(sid, "root package not contained in manifest")
        runtime, host_seeds, union, missing_paths = _two_closures(root_coord, meta_by_key)
        for key, path in missing_paths.items():
            package = packages.get(key)
            if package is None:
                kind = META_MISSING
            elif (not os.path.isfile(_object_path(root, package["meta_digest"]))
                  or not os.path.isfile(_object_path(root, package["archive_digest"]))):
                kind = CONTENT_MISSING
            else:
                kind = DIGEST_MISMATCH
            result.fail(
                sid, f"{_fmt(key)} {kind} path={_fmt_path(path)}"
            )
        for key in union:
            if key not in packages and key not in missing_paths:
                result.fail(sid, f"{_fmt(key)} reachable but absent from snapshot")
        declared_host_seeds = {tuple(x) for x in manifest.get("host_seeds", [])}
        if declared_host_seeds != host_seeds:
            result.fail(sid, "host seed set does not match independent closure")
    return result


def _fmt(key):
    return f"{key[0]}=={key[1]}@{key[2]}"


def _fmt_path(path):
    parts = [_fmt(_key(path[0]))]
    i = 1
    while i < len(path):
        parts.append(f"--{path[i]}-->")
        parts.append(_fmt(_key(path[i + 1])))
        i += 2
    return " ".join(parts)
