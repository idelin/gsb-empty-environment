"""The image repository: imports, snapshots, leases, garbage collection.

On-disk layout (created empty by :meth:`ImageStore.create`)::

    root/
      journal            framed, checksummed durability log
      catalog.json       materialised coordinate registry (derived cache)
      current            text file holding the current snapshot id
      objects/xx/<62>    SHA-256 addressed metadata and archive blobs
      staging/           verified-but-uncommitted import temp files
      snapshots/<id>.json immutable snapshot manifests
      leases/<token>.json explicit reader leases

Durability rule: nothing becomes visible until its journal record is
fsynced.  Materialised files are rebuildable by replaying the journal; the
catalog/current/lease files exist for cheap restart and stable leases.

Linearisation points (also documented in the package docstring):

import            journal append of ``import`` following verified objects
publish           os.replace of snapshots/<id>.json (record appended first)
set current       journal append of ``current``
lease acquire     journal append of ``lease-acquire``
lease release     journal append of ``lease-release``
lease recover     journal append of ``lease-recover``
gc mark           re-acquisition of the lock after GC_AFTER_MARK_LOCK hook;
                  the deletion decision for each object is re-evaluated under
                  the lock immediately before its unlink, so a snapshot that
                  became live between mark and sweep is never deleted
gc delete         os.unlink of one object
"""

import io
import json
import os
import secrets
import threading

from . import closure as closure_mod
from . import journal as journal_mod
from .content import ContentStore, digest_hex
from .errors import (
    DigestMismatch,
    JournalCorrupt,
    LeaseInvalid,
    MetadataConflict,
    MetadataInvalid,
    SnapshotExists,
    UnknownSnapshot,
)
from .events import (
    EventBus,
    GC_AFTER_MARK_LOCK,
    GC_AFTER_UNLINK,
    GC_BEFORE_UNLINK,
    GC_MARK_DONE,
    IMPORT_BEGIN_WINDOW,
    IMPORT_JOURNAL_APPENDED,
    IMPORT_STAGED,
    LEASE_ACQUIRED,
    LEASE_RECOVERED,
    LEASE_RELEASED,
    PUB_BEFORE_REPLACE,
    PUB_CHECKED,
    PUB_CURRENT_APPENDED,
    PUB_MANIFEST_REPLACED,
    PUB_TEMP_WRITTEN,
)

SCHEMA_VERSION = 1
VALID_LABELS = (closure_mod.RUN, closure_mod.HOST)


def canonical_json(obj):
    return json.dumps(obj, sort_keys=True, separators=(",", ":")).encode("utf-8")


def fsync_dir(path):
    try:
        fd = os.open(path, os.O_RDONLY)
    except OSError:
        return
    try:
        os.fsync(fd)
    except OSError:
        pass
    finally:
        os.close(fd)


def atomic_write(path, data, mode="wb"):
    """Write ``data`` to a temp file in the same dir and os.replace it."""
    directory = os.path.dirname(path) or "."
    os.makedirs(directory, exist_ok=True)
    tmp = os.path.join(directory, f".tmp-{secrets.token_hex(8)}")
    if "b" in mode:
        with open(tmp, "wb") as handle:
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
    else:
        with open(tmp, mode) as handle:
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
    os.replace(tmp, path)
    fsync_dir(directory)


class ImageStore:
    """All public operations are serialized by one re-entrant lock."""

    def __init__(self, root, bus=None):
        self.root = os.path.abspath(root)
        self.bus = bus if bus is not None else EventBus()
        self._lock = threading.RLock()
        self.objects = ContentStore(self.root)
        self.staging = os.path.join(self.root, "staging")
        self.snap_dir = os.path.join(self.root, "snapshots")
        self.lease_dir = os.path.join(self.root, "leases")
        self.journal_path = os.path.join(self.root, "journal")
        self.catalog_path = os.path.join(self.root, "catalog.json")
        self.current_path = os.path.join(self.root, "current")
        os.makedirs(self.staging, exist_ok=True)
        os.makedirs(self.snap_dir, exist_ok=True)
        os.makedirs(self.lease_dir, exist_ok=True)
        # In-memory state, always a projection of journal + object storage.
        self.catalog = {}           # key -> catalog entry dict
        self.snapshots = {}         # id -> manifest dict
        self.current = None         # current snapshot id
        self.leases = {}            # token -> {"snapshot": id}
        self.inflight = {}          # import_id -> [digests...]
        self.gc_generation = 0
        self.pending_gc = None      # plan being swept: {"generation", "set"}
        self.torn_tail_bytes = 0

    # ------------------------------------------------------------------ setup

    @classmethod
    def create(cls, root, bus=None):
        """Create an empty repository on disk and return an open instance."""
        os.makedirs(root, exist_ok=True)
        if os.listdir(root):
            raise FileExistsError(f"repository directory not empty: {root}")
        for sub in ("objects", "staging", "snapshots", "leases"):
            os.makedirs(os.path.join(root, sub), exist_ok=True)
        journal_path = os.path.join(root, "journal")
        with open(journal_path, "wb") as handle:
            handle.write(b"")
            handle.flush()
            os.fsync(handle.fileno())
        atomic_write(os.path.join(root, "catalog.json"), canonical_json({}))
        fsync_dir(root)
        store = cls(root, bus=bus)
        return store

    @classmethod
    def open(cls, root, bus=None):
        """Open a repository, performing process-crash recovery."""
        store = cls.__new__(cls)
        store.root = os.path.abspath(root)
        store.bus = bus if bus is not None else EventBus()
        store._lock = threading.RLock()
        store.objects = ContentStore(store.root)
        store.staging = os.path.join(store.root, "staging")
        store.snap_dir = os.path.join(store.root, "snapshots")
        store.lease_dir = os.path.join(store.root, "leases")
        store.journal_path = os.path.join(store.root, "journal")
        store.catalog_path = os.path.join(store.root, "catalog.json")
        store.current_path = os.path.join(store.root, "current")
        os.makedirs(store.staging, exist_ok=True)
        os.makedirs(store.snap_dir, exist_ok=True)
        os.makedirs(store.lease_dir, exist_ok=True)
        store.catalog = {}
        store.snapshots = {}
        store.current = None
        store.leases = {}
        store.inflight = {}
        store.gc_generation = 0
        store.pending_gc = None
        store._recover()
        return store

    def _journal(self, record):
        journal_mod.append(self.journal_path, record)

    # ------------------------------------------------------------ metadata I/O

    @staticmethod
    def _validate_meta(meta):
        if not isinstance(meta, dict):
            raise MetadataInvalid("metadata must be an object")
        for field in ("name", "version", "platform"):
            value = meta.get(field)
            if not isinstance(value, str) or not value:
                raise MetadataInvalid(f"metadata field {field!r} must be a string")
        for label in VALID_LABELS:
            deps = meta.get(label, [])
            if not isinstance(deps, list):
                raise MetadataInvalid(f"{label} must be a list")
            for dep in deps:
                if not isinstance(dep, dict):
                    raise MetadataInvalid(f"{label} entries must be objects")
                for field in ("name", "version", "platform"):
                    if not isinstance(dep.get(field), str) or not dep[field]:
                        raise MetadataInvalid(f"dependency {field!r} invalid")

    def _canonical_meta(self, meta):
        clean = {
            "schema": SCHEMA_VERSION,
            "name": meta["name"],
            "version": meta["version"],
            "platform": meta["platform"],
            "run_requires": [
                {k: d[k] for k in ("name", "version", "platform")}
                for d in meta.get("run_requires", [])
            ],
            "host_requires": [
                {k: d[k] for k in ("name", "version", "platform")}
                for d in meta.get("host_requires", [])
            ],
        }
        return canonical_json(clean), clean

    def _catalog_entry(self, coord, meta_digest, archive_digest, archive_length):
        return {
            "name": coord["name"],
            "version": coord["version"],
            "platform": coord["platform"],
            "meta_digest": meta_digest,
            "archive_digest": archive_digest,
            "archive_length": archive_length,
        }

    def _write_catalog(self):
        ordered = [self.catalog[key] for key in sorted(self.catalog)]
        atomic_write(self.catalog_path, canonical_json(ordered))

    # ----------------------------------------------------------------- imports

    def import_package(self, meta, archive_stream, declared_digest=None,
                       declared_length=None):
        """Import one package.

        ``archive_stream`` is a binary stream.  When ``declared_digest`` is
        omitted the digest is computed after streaming and no mismatch can
        occur; ``declared_length`` is always checked when supplied.  Metadata
        is canonicalised and stored under its own digest.  An uncommitted
        import is never visible to snapshots: visibility happens exactly at
        the durable ``import`` journal record.
        """
        self._validate_meta(meta)
        meta_bytes, clean_meta = self._canonical_meta(meta)
        meta_digest = digest_hex(meta_bytes)
        coord = {k: clean_meta[k] for k in ("name", "version", "platform")}
        key = closure_mod.coord_key(coord)

        with self._lock:
            existing = self.catalog.get(key)
            if existing is not None and existing["meta_digest"] != meta_digest:
                raise MetadataConflict(
                    f"{closure_mod.coord_str(coord)} already imported "
                    f"as {existing['meta_digest'][:12]}"
                )

            import_dir = os.path.join(self.staging, secrets.token_hex(8))
            os.makedirs(import_dir, exist_ok=True)
            try:
                archive_tmp, actual_digest, actual_length = self._stage_archive(
                    archive_stream, declared_digest, declared_length, import_dir
                )
            except BaseException:
                try:
                    os.rmdir(import_dir)
                except OSError:
                    pass
                raise
            if declared_digest is None:
                declared_digest = actual_digest
            else:
                declared_digest = declared_digest.lower()

            meta_tmp = os.path.join(import_dir, "meta.json")
            with open(meta_tmp, "wb") as handle:
                handle.write(meta_bytes)
                handle.flush()
                os.fsync(handle.fileno())

            # Already fully imported? idempotent success.
            if (existing is not None
                    and existing["archive_digest"] == declared_digest
                    and existing["archive_length"] == actual_length
                    and self.objects.exists(declared_digest)):
                self.objects.abandon(archive_tmp)
                self.objects.abandon(meta_tmp)
                os.rmdir(import_dir)
                return existing

            import_id = secrets.token_hex(8)
            staged_digests = [meta_digest]
            self.inflight[import_id] = staged_digests
            # import-begin makes the in-flight import a GC root: objects it
            # needs survive a collection even while the commit is pending.
            self._journal({
                "type": "import-begin", "import": import_id,
                "digests": staged_digests,
            })
            self.bus.fire(IMPORT_STAGED, {"import": import_id})

            # Lock-free window with the durable import-begin visible.  A
            # concurrent collection linearising here must keep the import's
            # declared digests.
            self._lock.release()
            try:
                self.bus.fire(IMPORT_BEGIN_WINDOW, {"import": import_id})
            finally:
                self._lock.acquire()

            committed_meta = self.objects.commit(meta_tmp, meta_digest)
            committed_archive = self.objects.commit(archive_tmp, declared_digest)
            staged_digests.append(declared_digest)
            try:
                os.rmdir(import_dir)
            except OSError:
                pass

            entry = self._catalog_entry(
                coord, meta_digest, declared_digest, actual_length
            )
            record = {
                "type": "import",
                "import": import_id,
                "entry": entry,
            }
            self._journal(record)
            # Linearisation point: the journal record is durable.
            self.catalog[key] = entry
            self.inflight.pop(import_id, None)
            self._write_catalog()
            self.bus.fire(IMPORT_JOURNAL_APPENDED, {"entry": entry})
            return entry

    def import_package_bytes(self, meta, data, declared_digest=None,
                             declared_length=None):
        return self.import_package(
            meta, io.BytesIO(data), declared_digest, declared_length
        )

    def _stage_archive(self, stream, declared_digest, declared_length,
                       import_dir, max_chunk=1 << 16):
        import hashlib
        import tempfile
        from .errors import LengthMismatch

        hasher = hashlib.sha256()
        total = 0
        fd, tmp = tempfile.mkstemp(prefix="archive-", dir=import_dir)
        try:
            with os.fdopen(fd, "wb") as out:
                while True:
                    chunk = stream.read(max_chunk)
                    if not chunk:
                        break
                    total += len(chunk)
                    if declared_length is not None and total > declared_length:
                        raise LengthMismatch(
                            f"stream exceeds declared length {declared_length}"
                        )
                    hasher.update(chunk)
                    out.write(chunk)
                out.flush()
                os.fsync(out.fileno())
        except BaseException:
            try:
                os.unlink(tmp)
            except FileNotFoundError:
                pass
            raise
        if declared_length is not None and total != declared_length:
            os.unlink(tmp)
            raise LengthMismatch(
                f"declared length {declared_length} != actual {total}"
            )
        actual = hasher.hexdigest()
        if declared_digest is not None and actual != declared_digest.lower():
            os.unlink(tmp)
            raise DigestMismatch(
                f"declared digest {declared_digest} != actual {actual}"
            )
        return tmp, actual, total


    # ------------------------------------------------------------- resolution

    def _resolve_coord(self, coord):
        """Closure resolver returning status tuples for ``evaluate_closures``."""
        key = closure_mod.coord_key(coord)
        entry = self.catalog.get(key)
        if entry is None:
            return ("metadata-missing",)
        meta = self._load_canonical_meta(entry["meta_digest"])
        archive_path = self.objects.path(entry["archive_digest"])
        if not os.path.isfile(archive_path):
            return ("content-missing", meta)
        if not self.objects.verify(entry["archive_digest"]):
            return ("digest-mismatch", meta)
        return ("ok", meta)

    def _load_canonical_meta(self, digest):
        with self.objects.open_blob(digest) as handle:
            return json.loads(handle.read())

    def check_publish(self, root):
        """Compute the closure report used both by publish and dry checks."""
        with self._lock:
            report = closure_mod.evaluate_closures(root, self._resolve_coord)
            return report

    def closure_objects(self, root):
        """Return the report plus catalog entries of every reachable node."""
        report = self.check_publish(root)
        entries = {}
        for key in report.reachable_keys:
            entries[key] = self.catalog.get(key)
        return report, entries

    # -------------------------------------------------------------- snapshots

    def publish_snapshot(self, root, snapshot_id=None, make_current=True):
        """Publish an immutable snapshot after both closures verify.

        Ordering: closure check (under lock) -> temp manifest -> durable
        journal record -> os.replace (linearisation point) -> optional
        current record.  A crash before the replace leaves only an orphan
        temp file and a journal record already naming the manifest; recovery
        materialises the manifest from the journal.
        """
        with self._lock:
            report = closure_mod.evaluate_closures(root, self._resolve_coord)
            if not report.complete:
                raise _closure_error(report)
            self.bus.fire(PUB_CHECKED, {"root": root})

            packages = []
            for key in sorted(report.reachable_keys):
                entry = self.catalog[key]
                packages.append({
                    "name": entry["name"],
                    "version": entry["version"],
                    "platform": entry["platform"],
                    "meta_digest": entry["meta_digest"],
                    "archive_digest": entry["archive_digest"],
                    "archive_length": entry["archive_length"],
                })
            root_entry = self.catalog[closure_mod.coord_key(root)]
            manifest = {
                "schema": SCHEMA_VERSION,
                "snapshot": snapshot_id,
                "root": {k: root_entry[k] for k in ("name", "version", "platform")},
                "runtime": [list(key) for key in sorted(report.runtime_keys)],
                "host_seeds": [list(key) for key in sorted(report.host_seed_keys)],
                "host_only": [list(key) for key in sorted(report.host_keys)],
                "packages": packages,
            }
            if snapshot_id is None:
                snapshot_id = "snap-" + secrets.token_hex(8)
            manifest["snapshot"] = snapshot_id
            if snapshot_id in self.snapshots:
                raise SnapshotExists(f"snapshot already published: {snapshot_id}")
            manifest_bytes = canonical_json(manifest)

            temp_path = os.path.join(
                self.snap_dir, f".tmp-{secrets.token_hex(8)}"
            )
            with open(temp_path, "wb") as handle:
                handle.write(manifest_bytes)
                handle.flush()
                os.fsync(handle.fileno())
            self.bus.fire(PUB_TEMP_WRITTEN, {"path": temp_path})

            self._journal({
                "type": "snapshot-publish",
                "snapshot": snapshot_id,
                "manifest": manifest_bytes.decode("utf-8"),
            })
            self.snapshots[snapshot_id] = manifest
            final_path = os.path.join(self.snap_dir, f"{snapshot_id}.json")
            self.bus.fire(PUB_BEFORE_REPLACE, {"snapshot": snapshot_id,
                                               "temp": temp_path})
            os.replace(temp_path, final_path)
            fsync_dir(self.snap_dir)
            # Linearisation point: the manifest is now readable on disk.
            self.bus.fire(PUB_MANIFEST_REPLACED, {"snapshot": snapshot_id})

            if make_current:
                self._journal({"type": "current", "snapshot": snapshot_id})
                self.current = snapshot_id
                atomic_write(self.current_path, snapshot_id.encode("utf-8"))
                self.bus.fire(PUB_CURRENT_APPENDED, {"snapshot": snapshot_id})
            return snapshot_id

    def snapshot_manifest(self, snapshot_id):
        with self._lock:
            if snapshot_id not in self.snapshots:
                raise UnknownSnapshot(snapshot_id)
            return json.loads(json.dumps(self.snapshots[snapshot_id]))

    def list_snapshots(self):
        with self._lock:
            return sorted(self.snapshots)

    def current_snapshot(self):
        with self._lock:
            return self.current

    def open_snapshot(self, snapshot_id):
        with self._lock:
            if snapshot_id not in self.snapshots:
                raise UnknownSnapshot(snapshot_id)
            return SnapshotView(self, snapshot_id)


    # ----------------------------------------------------------------- leases

    def acquire_lease(self, snapshot_id=None):
        """Grant an explicit reader lease on a snapshot.

        Leases never expire by wall-clock time.  They terminate only through
        :meth:`release_lease` or :meth:`recover_lease`, both of which append a
        durable terminating record.
        """
        with self._lock:
            if snapshot_id is None:
                if self.current is None:
                    raise UnknownSnapshot("no current snapshot to lease")
                snapshot_id = self.current
            if snapshot_id not in self.snapshots:
                raise UnknownSnapshot(snapshot_id)
            token = "lease-" + secrets.token_hex(12)
            self._journal({
                "type": "lease-acquire", "lease": token, "snapshot": snapshot_id,
            })
            self.leases[token] = {"snapshot": snapshot_id}
            self._materialize_lease(token, snapshot_id)
            self.bus.fire(LEASE_ACQUIRED, {"lease": token, "snapshot": snapshot_id})
            return Lease(self, token, snapshot_id)

    def release_lease(self, token):
        with self._lock:
            if token not in self.leases:
                raise LeaseInvalid(f"unknown or terminated lease: {token}")
            snapshot_id = self.leases[token]["snapshot"]
            self._journal({
                "type": "lease-release", "lease": token, "snapshot": snapshot_id,
            })
            del self.leases[token]
            self._remove_lease_file(token)
            # Linearisation point: after the durable release record.
            self.bus.fire(LEASE_RELEASED, {"lease": token, "snapshot": snapshot_id})

    def recover_lease(self, token):
        """Terminate a lease via the explicit recovery protocol.

        This models an operator/reader confirming its work is done after a
        crash; unlike release it tolerates a lease whose materialised file was
        lost, as long as the durable acquire record still shows it live.
        """
        with self._lock:
            if token not in self.leases:
                raise LeaseInvalid(f"unknown or terminated lease: {token}")
            snapshot_id = self.leases[token]["snapshot"]
            self._journal({
                "type": "lease-recover", "lease": token, "snapshot": snapshot_id,
            })
            del self.leases[token]
            self._remove_lease_file(token)
            self.bus.fire(LEASE_RECOVERED, {"lease": token, "snapshot": snapshot_id})

    def reader(self, lease):
        """Bind a :class:`Lease` to a live :class:`Reader` view."""
        with self._lock:
            token = lease.token if isinstance(lease, Lease) else lease
            if token not in self.leases:
                raise LeaseInvalid(f"lease no longer valid: {token}")
            snapshot_id = self.leases[token]["snapshot"]
            return Reader(self, token, snapshot_id)

    def active_leases(self):
        with self._lock:
            return {token: info["snapshot"] for token, info in self.leases.items()}

    def _materialize_lease(self, token, snapshot_id):
        path = os.path.join(self.lease_dir, f"{token}.json")
        atomic_write(path, canonical_json({"lease": token, "snapshot": snapshot_id}))

    def _remove_lease_file(self, token):
        try:
            os.unlink(os.path.join(self.lease_dir, f"{token}.json"))
        except FileNotFoundError:
            pass
        fsync_dir(self.lease_dir)

    # ------------------------------------------------------------- GC roots

    def _snapshot_digests(self, snapshot_id):
        digests = set()
        manifest = self.snapshots[snapshot_id]
        for package in manifest["packages"]:
            digests.add(package["meta_digest"])
            digests.add(package["archive_digest"])
        return digests

    def _live_digests(self):
        """Compute the generational GC root set.

        Roots: objects of the current snapshot, objects of every snapshot
        pinned by a valid lease, and every digest registered by an in-flight
        import (its ``import-begin`` record is already durable).
        """
        live = set()
        if self.current is not None:
            live |= self._snapshot_digests(self.current)
        pinned = {info["snapshot"] for info in self.leases.values()}
        for snapshot_id in pinned:
            live |= self._snapshot_digests(snapshot_id)
        for digests in self.inflight.values():
            live.update(digests)
        return live


    # ------------------------------------------------------- garbage collect

    def collect_garbage(self):
        """Run one generational mark/sweep collection.

        Mark phase runs under the lock and journals ``gc-begin`` with the
        generation.  The GC_AFTER_MARK_LOCK seam and every inter-object yield
        run *without* the lock so concurrent publish/release operations can
        linearise between mark and sweep; each object is nevertheless
        re-tested against the current live set under the lock immediately
        before its unlink.  A snapshot published (or lease retained) after
        the mark therefore cannot be deleted by this sweep.
        """
        from .events import GC_SWEEP_YIELD
        from .errors import PendingGC

        with self._lock:
            if self.pending_gc is not None:
                plan = self.pending_gc
                generation = plan["generation"]
                self.bus.fire(GC_MARK_DONE, {"generation": generation,
                                             "resumed": True})
            else:
                generation = self.gc_generation + 1
                live = self._live_digests()
                candidates = sorted(self.objects.all_digests() - live)
                plan = {
                    "generation": generation,
                    "remaining": candidates,
                    "retained": [],
                    "deleted": [],
                }
                self.gc_generation = generation
                self.pending_gc = plan
                self._journal({"type": "gc-begin", "generation": generation})
                self.bus.fire(GC_MARK_DONE, {
                    "generation": generation,
                    "candidates": list(candidates),
                })
        # Seam outside the lock: concurrent publishers/releasers run here.
        self.bus.fire(GC_AFTER_MARK_LOCK, {"generation": generation})
        return self._sweep(plan)

    def _sweep(self, plan):
        from .events import GC_SWEEP_YIELD
        from .errors import PendingGC

        while True:
            with self._lock:
                if not plan["remaining"]:
                    self._journal({"type": "gc-end",
                                   "generation": plan["generation"]})
                    self.pending_gc = None
                    return {
                        "generation": plan["generation"],
                        "deleted": list(plan["deleted"]),
                        "retained": list(plan["retained"]),
                    }
                digest = plan["remaining"].pop(0)
                if digest in self._live_digests():
                    plan["retained"].append(digest)
                    continue
                detail = {"generation": plan["generation"], "digest": digest}
                self.bus.fire(GC_BEFORE_UNLINK, detail)
                self.objects.delete(digest)
                shard = os.path.join(self.objects.objects, digest[:2])
                fsync_dir(shard)
                forgotten = self._forget_entries_for(digest)
                self._journal({
                    "type": "gc-delete",
                    "generation": plan["generation"],
                    "digest": digest,
                    "forget": forgotten,
                })
                plan["deleted"].append(digest)
                self.bus.fire(GC_AFTER_UNLINK, detail)
            # Deterministic interleave seam, lock not held.
            self.bus.fire(GC_SWEEP_YIELD, {"remaining": plan["remaining"]})

    def _forget_entries_for(self, deleted_digest):
        """Drop catalog entries made unreachable by ``deleted_digest``."""
        live = self._live_digests()
        forgotten = []
        for key, entry in list(self.catalog.items()):
            if entry["meta_digest"] in live or entry["archive_digest"] in live:
                continue
            if (deleted_digest == entry["meta_digest"]
                    or deleted_digest == entry["archive_digest"]):
                del self.catalog[key]
                forgotten.append(list(key))
        if forgotten:
            self._write_catalog()
        return forgotten


    # ---------------------------------------------------------------- recovery

    def _recover(self):
        """Rebuild all derived state from the durable journal.

        Committed snapshots are materialised from their journal records;
        orphan temp manifests (a crash after temp write but before the commit
        record or before replace) are deleted.  A torn tail record is
        truncated, mid-log corruption is refused.  Unfinished imports are
        aborted and their unreferenced objects swept.
        """
        records, torn = journal_mod.read_journal(self.journal_path)
        self.torn_tail_bytes = torn
        max_generation = 0
        gc_finished = set()
        gc_deleted = {}
        for record in records:
            rtype = record.get("type")
            if rtype == "import-begin":
                self.inflight[record["import"]] = list(record.get("digests", []))
            elif rtype == "import":
                entry = record["entry"]
                key = (entry["name"], entry["version"], entry["platform"])
                self.catalog[key] = entry
                self.inflight.pop(record.get("import"), None)
            elif rtype == "snapshot-publish":
                manifest = json.loads(record["manifest"])
                self.snapshots[manifest["snapshot"]] = manifest
            elif rtype == "current":
                self.current = record["snapshot"]
            elif rtype == "lease-acquire":
                self.leases[record["lease"]] = {"snapshot": record["snapshot"]}
            elif rtype in ("lease-release", "lease-recover"):
                self.leases.pop(record["lease"], None)
            elif rtype == "gc-begin":
                max_generation = max(max_generation, record["generation"])
                gc_deleted.setdefault(record["generation"], [])
            elif rtype == "gc-delete":
                gen = record["generation"]
                max_generation = max(max_generation, gen)
                gc_deleted.setdefault(gen, []).append(record["digest"])
                for key_tuple in record.get("forget", ()):
                    self.catalog.pop(tuple(key_tuple), None)
            elif rtype == "gc-end":
                gc_finished.add(record["generation"])
            elif rtype == "import-abort":
                self.inflight.pop(record.get("import"), None)

        self.gc_generation = max_generation
        unfinished = sorted(
            gen for gen in gc_deleted if gen not in gc_finished
        )
        for gen in unfinished:
            already = set(gc_deleted[gen])
            self._journal({"type": "gc-abandon", "generation": gen})
            # Objects deleted before the crash are already gone; the rest of
            # that stale plan is simply discarded and a new collection may
            # collect them later.
        if unfinished:
            self.gc_generation = max_generation
        self.pending_gc = None

        self._rebuild_snapshots()
        self._rebuild_current()
        self._rebuild_leases()
        self._sweep_staging()
        base_retained = self._base_retained_digests()
        self._abort_inflight_imports(base_retained)
        self._sweep_orphan_objects(base_retained)
        self._write_catalog()

    def _rebuild_snapshots(self):
        """Make snapshots/ match journal state; delete orphan temp files."""
        if not os.path.isdir(self.snap_dir):
            os.makedirs(self.snap_dir, exist_ok=True)
        live_files = set()
        for snapshot_id, manifest in self.snapshots.items():
            data = canonical_json(manifest)
            final = os.path.join(self.snap_dir, f"{snapshot_id}.json")
            if not os.path.exists(final):
                # Commit record existed but the rename crashed: complete it.
                tmp = os.path.join(self.snap_dir, f".recover-{secrets.token_hex(4)}")
                with open(tmp, "wb") as handle:
                    handle.write(data)
                    handle.flush()
                    os.fsync(handle.fileno())
                os.replace(tmp, final)
            live_files.add(f"{snapshot_id}.json")
        for name in os.listdir(self.snap_dir):
            if name not in live_files:
                os.unlink(os.path.join(self.snap_dir, name))
        fsync_dir(self.snap_dir)

    def _rebuild_current(self):
        if self.current is not None:
            atomic_write(self.current_path, self.current.encode("utf-8"))
        elif os.path.exists(self.current_path):
            os.unlink(self.current_path)

    def _rebuild_leases(self):
        if not os.path.isdir(self.lease_dir):
            os.makedirs(self.lease_dir, exist_ok=True)
        live = {f"{token}.json" for token in self.leases}
        for name in os.listdir(self.lease_dir):
            if name not in live:
                try:
                    os.unlink(os.path.join(self.lease_dir, name))
                except FileNotFoundError:
                    pass
        for token, info in self.leases.items():
            path = os.path.join(self.lease_dir, f"{token}.json")
            if not os.path.exists(path):
                atomic_write(path, canonical_json(
                    {"lease": token, "snapshot": info["snapshot"]}))
        fsync_dir(self.lease_dir)

    def _sweep_staging(self):
        import shutil
        if not os.path.isdir(self.staging):
            os.makedirs(self.staging, exist_ok=True)
            return
        for name in os.listdir(self.staging):
            path = os.path.join(self.staging, name)
            if os.path.isdir(path):
                shutil.rmtree(path, ignore_errors=True)
            else:
                try:
                    os.unlink(path)
                except FileNotFoundError:
                    pass

    def _base_retained_digests(self):
        retained = set()
        for entry in self.catalog.values():
            retained.add(entry["meta_digest"])
            retained.add(entry["archive_digest"])
        for snapshot_id in self.snapshots:
            retained |= self._snapshot_digests(snapshot_id)
        return retained

    def _sweep_orphan_objects(self, retained):
        for digest in list(self.objects.all_digests()):
            if digest not in retained:
                self.objects.delete(digest)

    def _abort_inflight_imports(self, base_retained):
        for import_id, digests in list(self.inflight.items()):
            self._journal({"type": "import-abort", "import": import_id})
            for digest in digests:
                if digest not in base_retained:
                    self.objects.delete(digest)
        self.inflight.clear()


def _closure_error(report):
    from .errors import ClosureIncomplete
    summary = ", ".join(
        f"{issue['kind']}: {closure_mod.coord_str(issue['coordinate'])}"
        for issue in report.issues
    )
    return ClosureIncomplete(
        f"closure incomplete ({len(report.issues)} items): {summary}",
        [dict(issue) for issue in report.issues],
    )


class Lease:
    """An explicit, non-expiring reader lease token."""

    def __init__(self, store, token, snapshot_id):
        self._store = store
        self.token = token
        self.snapshot_id = snapshot_id

    @property
    def valid(self):
        with self._store._lock:
            return self.token in self._store.leases

    def release(self):
        self._store.release_lease(self.token)

    def recover(self):
        self._store.recover_lease(self.token)

    def __repr__(self):
        return f"<Lease {self.token} -> {self.snapshot_id}>"


class Reader:
    """Reads packages through a valid lease.

    Every read is linearised while holding the repository lock and re-checks
    the lease, so a read whose release linearises first fails while reads that
    started earlier under a valid lease succeed.
    """

    def __init__(self, store, token, snapshot_id):
        self._store = store
        self.token = token
        self.snapshot_id = snapshot_id

    def _entry(self, coord):
        with self._store._lock:
            if self.token not in self._store.leases:
                raise LeaseInvalid(f"lease terminated: {self.token}")
            if self._store.leases[self.token]["snapshot"] != self.snapshot_id:
                raise LeaseInvalid("lease snapshot binding changed")
            key = closure_mod.coord_key(coord)
            for package in self._store.snapshots[self.snapshot_id]["packages"]:
                pkey = (package["name"], package["version"], package["platform"])
                if pkey == key:
                    return package
            raise KeyError(f"{closure_mod.coord_str(coord)} not in snapshot")

    def has_package(self, coord):
        try:
            self._entry(coord)
            return True
        except KeyError:
            return False

    def package_names(self):
        with self._store._lock:
            if self.token not in self._store.leases:
                raise LeaseInvalid(f"lease terminated: {self.token}")
            return [p["name"] for p in
                    self._store.snapshots[self.snapshot_id]["packages"]]

    def read_archive(self, coord, verify=True):
        entry = self._entry(coord)
        if verify and not self._store.objects.verify(entry["archive_digest"]):
            raise DigestMismatch("stored archive failed verification")
        with self._store.objects.open_blob(entry["archive_digest"]) as handle:
            return handle.read()

    def read_meta(self, coord):
        entry = self._entry(coord)
        with self._store.objects.open_blob(entry["meta_digest"]) as handle:
            return json.loads(handle.read())

    def open_archive(self, coord, verify=True):
        """Return an open binary handle (caller closes it)."""
        entry = self._entry(coord)
        if verify and not self._store.objects.verify(entry["archive_digest"]):
            raise DigestMismatch("stored archive failed verification")
        return self._store.objects.open_blob(entry["archive_digest"])


class SnapshotView:
    """Lease-free introspection of a published snapshot manifest."""

    def __init__(self, store, snapshot_id):
        self._store = store
        self.snapshot_id = snapshot_id

    @property
    def manifest(self):
        return self._store.snapshot_manifest(self.snapshot_id)

    def coordinates(self):
        return [
            {"name": p["name"], "version": p["version"], "platform": p["platform"]}
            for p in self.manifest["packages"]
        ]
