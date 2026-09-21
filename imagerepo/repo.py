"""Offline image repository: import, publish, lease, generational GC.

Concurrency model
-----------------
One in-process :class:`threading.RLock` serializes structural state changes.
The journal is the durable linearization record; every mutating operation has
a single linearization point (``LP`` below): the durable append of its
decision record or the atomic replacement it performs.

Generational GC fence
---------------------
``GC_BEGIN`` is the GC scan LP. While holding the lock it enumerates every
on-disk object/manifest and every live intent (in-flight imports, snapshot
commits, leases, the current snapshot) and appends the fenced candidate set.
Snapshot *begins* after the fence block until that generation finishes, so a
snapshot published after the mark can never reference an object this
generation sweeps. Each unlink also re-fences current live roots, so an
import begun after the scan retains its objects.

Leases never expire on a wall clock; they end at LEASE_RELEASE or via an
explicit recovery protocol (``reap_leases``).
"""
from __future__ import annotations

import hashlib
import json
import os
import shutil
import threading
import uuid
from dataclasses import dataclass, field

from .closure import (
    CONTENT_MISSING,
    DIGEST_MISMATCH,
    METADATA_MISSING,
    Gap,
)
from .journal import Journal, atomic_write, fsync_dir
from .model import HOST_PLATFORM, Dep, Ident, Metadata, canonical_json

T_IMPORT_BEGIN = "IMPORT_BEGIN"
T_IMPORT_COMMIT = "IMPORT_COMMIT"
T_IMPORT_ABORT = "IMPORT_ABORT"
T_SNAP_BEGIN = "SNAP_BEGIN"
T_SNAP_COMMIT = "SNAP_COMMIT"
T_SNAP_ABORT = "SNAP_ABORT"
T_LEASE_GRANT = "LEASE_GRANT"
T_LEASE_RELEASE = "LEASE_RELEASE"
T_GC_BEGIN = "GC_BEGIN"
T_GC_UNLINK = "GC_UNLINK"
T_GC_DONE = "GC_DONE"


class RepoError(Exception):
    pass


class CrashInjected(Exception):
    """Raised by fault hooks to simulate a process crash mid-operation."""


class IncompleteSnapshot(RepoError):
    def __init__(self, gaps):
        self.gaps = gaps
        super().__init__(f"snapshot incomplete: {len(gaps)} gap(s)")


class ImmutabilityViolation(RepoError):
    pass


class LeaseInvalid(RepoError):
    pass


class FaultBus:
    """Inject faults on named event points.

    A hook is called ``hook(event)`` immediately before the boundary the name
    describes. Raising :class:`CrashInjected` simulates a hard process crash:
    no cleanup runs, the Repository instance is abandoned, and reopening runs
    crash recovery.
    """

    def __init__(self):
        self._hooks = {}
        self.log = []

    def set(self, event, hook=None):
        self._hooks[event] = hook

    def raise_on(self, event, once=True):
        def hook(_ev):
            if once:
                self._hooks.pop(event, None)
            raise CrashInjected(event)
        self._hooks[event] = hook
        return hook

    def fire(self, event):
        self.log.append(event)
        hook = self._hooks.get(event)
        if hook is not None:
            hook(event)


def _sha256_file(path, expected_length=None):
    h = hashlib.sha256()
    total = 0
    with open(path, "rb") as fh:
        while True:
            chunk = fh.read(1 << 20)
            if not chunk:
                break
            total += len(chunk)
            h.update(chunk)
    if expected_length is not None and total != expected_length:
        raise RepoError(f"length mismatch: got {total}, declared {expected_length}")
    return h.hexdigest(), total


@dataclass
class _ImportIntent:
    import_id: str
    ident: Ident
    meta: Metadata
    staged: bool = False
    committed: bool = False


@dataclass
class _SnapIntent:
    snap_id: str
    roots: tuple[Ident, ...]
    objects: tuple[str, ...]
    seq: int = -1
    manifest_written: bool = False


@dataclass
class GcReport:
    generation: int
    scanned_objects: int
    scanned_manifests: int
    retained_objects: int
    dropped_objects: list[str] = field(default_factory=list)
    dropped_manifests: list[str] = field(default_factory=list)


class Repository:
    def __init__(self, root: str, bus: FaultBus | None = None):
        self.root = os.path.abspath(root)
        self.bus = bus or FaultBus()
        self.objects_dir = os.path.join(self.root, "objects")
        self.meta_dir = os.path.join(self.root, "meta")
        self.snaps_dir = os.path.join(self.root, "snapshots")
        self.leases_dir = os.path.join(self.root, "leases")
        self.stage_dir = os.path.join(self.root, "stage")
        self.tmp_dir = os.path.join(self.root, "tmp")
        self.current_path = os.path.join(self.root, "current.json")
        self.lock = threading.RLock()
        self._idle = threading.Condition(self.lock)
        self._active_gcs: set[int] = set()
        self._seq = 0
        self._imports: dict[str, _ImportIntent] = {}
        self._committed_idents: set[Ident] = set()
        self._snaps: dict[str, _SnapIntent] = {}
        self._leases: dict[str, dict] = {}
        self._journal: Journal | None = None

    # ---------------------------------------------------------------- paths
    def object_path(self, digest: str, length: int | None = None) -> str:
        return os.path.join(self.objects_dir, digest[:2], digest)

    def has_object(self, digest: str, length: int) -> bool:
        path = self.object_path(digest)
        try:
            return os.path.getsize(path) == length
        except OSError:
            return False

    def meta_path(self, ident: Ident) -> str:
        return os.path.join(self.meta_dir, ident.key + ".json")

    def snap_path(self, snap_id: str) -> str:
        return os.path.join(self.snaps_dir, snap_id + ".json")

    def lease_path(self, lease_id: str) -> str:
        return os.path.join(self.leases_dir, lease_id + ".json")

    def get_metadata(self, ident: Ident) -> Metadata | None:
        path = self.meta_path(ident)
        if not os.path.exists(path):
            return None
        with open(path, "rb") as fh:
            return Metadata.from_dict(json.loads(fh.read()))

    def snapshot_roots(self, snap_id: str) -> tuple[Ident, ...]:
        with open(self.snap_path(snap_id), "rb") as fh:
            data = json.loads(fh.read())
        return tuple(Ident(**r) for r in data["roots"])

    def current_snapshot(self) -> str | None:
        if not os.path.exists(self.current_path):
            return None
        with open(self.current_path, "rb") as fh:
            return json.loads(fh.read())["snapshot"]

    # ------------------------------------------------------------- open/recover
    @classmethod
    def open(cls, root: str, bus: FaultBus | None = None) -> "Repository":
        repo = cls(root, bus)
        for d in (repo.objects_dir, repo.meta_dir, repo.snaps_dir,
                  repo.leases_dir, repo.stage_dir, repo.tmp_dir):
            os.makedirs(d, exist_ok=True)
        repo._journal = Journal.open(repo.root)
        from .journal import scan_journal
        records, _, truncated = scan_journal(os.path.join(repo.root, "journal.log"))
        repo._truncated_on_open = truncated
        repo._replay(records)
        repo._reconcile_fs()
        return repo

    def _replay(self, records) -> None:
        for rec in records:
            self._seq = rec.seq + 1
            p = rec.payload
            kind = p["type"]
            if kind == T_IMPORT_BEGIN:
                ident = Ident(**p["ident"])
                self._imports[p["import_id"]] = _ImportIntent(
                    p["import_id"], ident, Metadata.from_dict(p["meta"]),
                    staged=p.get("staged", False),
                )
            elif kind == T_IMPORT_COMMIT:
                ident = Ident(**p["ident"])
                intent = self._imports.get(p["import_id"])
                if intent is None and "meta" in p:
                    intent = _ImportIntent(p["import_id"], ident,
                                           Metadata.from_dict(p["meta"]),
                                           staged=True, committed=True)
                    self._imports[p["import_id"]] = intent
                if intent is not None:
                    intent.committed = True
                    self._committed_idents.add(intent.ident)
            elif kind == T_IMPORT_ABORT:
                self._imports.pop(p["import_id"], None)
            elif kind == T_SNAP_BEGIN:
                ident_roots = tuple(Ident(**r) for r in p["roots"])
                self._snaps[p["snap_id"]] = _SnapIntent(
                    p["snap_id"], ident_roots, tuple(p["objects"]),
                    seq=rec.seq,
                )
            elif kind == T_SNAP_COMMIT:
                self._snaps.pop(p["snap_id"], None)
            elif kind == T_SNAP_ABORT:
                self._snaps.pop(p["snap_id"], None)
            elif kind == T_LEASE_GRANT:
                self._leases[p["lease_id"]] = {
                    "snapshot": p["snapshot"], "holder": p["holder"],
                }
            elif kind == T_LEASE_RELEASE:
                self._leases.pop(p["lease_id"], None)
            elif kind == T_GC_BEGIN:
                self._active_gcs.add(p["generation"])
            elif kind == T_GC_DONE:
                self._active_gcs.discard(p["generation"])

    def _append(self, payload: dict):
        return self._journal.append(payload)

    # --------------------------------------------------------- fs reconciliation
    def _reconcile_fs(self) -> None:
        """Redo/undo filesystem effects so journal state and disk agree.

        Order matters: committed snapshots become visible first, then leases,
        then abandoned GC work is finished; last, staging junk is removed.
        """
        # 1. Imports: a committed intent redelivers its metadata; intents
        #    without a durable COMMIT are aborted later (blob left as garbage).
        for intent in list(self._imports.values()):
            meta_path = self.meta_path(intent.ident)
            if intent.committed:
                if not os.path.exists(meta_path):
                    atomic_write(meta_path, canonical_json(intent.meta.to_dict()),
                                 tmp_dir=self.tmp_dir)
                self._imports.pop(intent.import_id, None)
        # Remove metadata files for which no IMPORT_COMMIT exists and which no
        # already-committed snapshot references (snapshot objects are also
        # journal-committed, but the snapshot itself proves reachability).
        committed_snaps, begun_snaps = self._snapshot_journal_state()
        referenced = set(self._committed_idents)
        for snap_id in committed_snaps:
            if os.path.exists(self.snap_path(snap_id)):
                with open(self.snap_path(snap_id), "rb") as fh:
                    data = json.loads(fh.read())
                for m in data.get("metadata", {}).values():
                    referenced.add(Ident(m["name"], m["version"], m["platform"]))
        for dirpath, _dirs, files in os.walk(self.meta_dir):
            for name in files:
                if not name.endswith(".json"):
                    continue
                ident = Ident.from_key(name[:-5])
                if ident not in referenced:
                    os.unlink(os.path.join(dirpath, name))
        # 2. Snapshots. A begun-but-uncommitted manifest is an orphan unless
        #    current.json already points at it: the atomic replacement is the
        #    publish LP, so that snapshot is committed and we backfill COMMIT.
        snap_files = {f[:-5] for f in os.listdir(self.snaps_dir) if f.endswith(".json")}
        current = None
        if os.path.exists(self.current_path):
            with open(self.current_path, "rb") as fh:
                current = json.loads(fh.read())["snapshot"]
        if current is not None and current in begun_snaps:
            self._append({"type": T_SNAP_COMMIT, "snap_id": current})
            committed_snaps.add(current)
            begun_snaps.discard(current)
            self._snaps.pop(current, None)
        if current is not None and not os.path.exists(self.snap_path(current)):
            raise RepoError("current.json points at a missing snapshot; refusing recovery")
        for snap_id in begun_snaps:
            if snap_id == current:
                continue
            self._append({"type": T_SNAP_ABORT, "snap_id": snap_id})
            self._snaps.pop(snap_id, None)
            path = self.snap_path(snap_id)
            if os.path.exists(path):
                os.unlink(path)
        for snap_id in snap_files - committed_snaps:
            path = self.snap_path(snap_id)
            if os.path.exists(path) and snap_id != current:
                os.unlink(path)
        # 3. Leases: keep grant files; delete released or stray ones.
        lease_files = {f[:-5] for f in os.listdir(self.leases_dir) if f.endswith(".json")}
        for lease_id in lease_files - set(self._leases):
            os.unlink(self.lease_path(lease_id))
        for lease_id, info in self._leases.items():
            path = self.lease_path(lease_id)
            if not os.path.exists(path):
                self._write_lease_file(lease_id, info["snapshot"], info["holder"])
            elif not os.path.exists(self.snap_path(info["snapshot"])):
                # Lease references an orphaned snapshot — impossible unless the
                # lease release raced its own manifest loss; drop defensively.
                os.unlink(path)
                self._leases.pop(lease_id, None)
        # 4. Finish any GC generation that crashed mid-sweep.
        for gen in sorted(self._active_gcs):
            self._resume_gc(gen)
        # 4b. Any import still in-flight after a crash is an orphan intent:
        #     append ABORT. Its blob, if published, is plain content-addressed
        #     garbage and will be swept by the next GC; its staged meta was
        #     already removed above.
        for import_id, intent in list(self._imports.items()):
            self._append({"type": T_IMPORT_ABORT, "import_id": import_id})
            self._imports.pop(import_id, None)
        # 5. Temp/staging cleanup (whole-directory wipe is safe: nothing live
        #    ever resides there — published objects live under objects/).
        for directory in (self.stage_dir, self.tmp_dir):
            for entry in os.listdir(directory):
                path = os.path.join(directory, entry)
                if os.path.isdir(path):
                    shutil.rmtree(path)
                else:
                    os.unlink(path)
        fsync_dir(self.root)

    def _snapshot_journal_state(self) -> tuple[set[str], set[str]]:
        committed, begun = set(), set()
        for rec in self._journal.records():
            kind = rec.payload["type"]
            if kind == T_SNAP_BEGIN:
                begun.add(rec.payload["snap_id"])
            elif kind == T_SNAP_COMMIT:
                committed.add(rec.payload["snap_id"])
                begun.discard(rec.payload["snap_id"])
            elif kind == T_SNAP_ABORT:
                begun.discard(rec.payload["snap_id"])
        return committed, begun

    def _write_lease_file(self, lease_id, snapshot, holder):
        atomic_write(self.lease_path(lease_id), canonical_json(
            {"lease": lease_id, "snapshot": snapshot, "holder": holder}),
            tmp_dir=self.tmp_dir)

    # -------------------------------------------------------------- closure
    def _resolve_closure(self, roots):
        """Repo-local closure BFS; returns (runtime, build, gaps).

        Kept separate from :mod:`imagerepo.closure` (the independent oracle)
        on purpose: tests cross-check the two implementations.
        """
        gaps: list[Gap] = []
        runtime: set[Ident] = set()
        build: set[Ident] = set()
        seen_gap: set = set()

        def add_gap(kind, ident, path, detail=""):
            marker = (kind, ident, tuple(path))
            if marker not in seen_gap:
                seen_gap.add(marker)
                gaps.append(Gap(kind, ident, tuple(path), detail))

        def walk(start, *, host_scope=False, start_path=None):
            reached: set[Ident] = set()
            queued = {start}
            work = [(start, start_path or (start,))]
            ok = True
            while work:
                ident, path = work.pop(0)
                if ident in reached:
                    continue
                meta = self.get_metadata(ident)
                if meta is None:
                    add_gap(METADATA_MISSING, ident, path)
                    ok = False
                    continue
                if not self.has_object(meta.archive_digest, meta.archive_length):
                    add_gap(CONTENT_MISSING, ident, path, meta.archive_digest[:12])
                    ok = False
                    continue
                reached.add(ident)
                if host_scope:
                    children = [
                        Ident(d.name, d.version, HOST_PLATFORM)
                        for d in list(meta.runtime_deps) + list(meta.build_deps)
                    ]
                else:
                    children = [Ident(d.name, d.version, ident.platform)
                                for d in meta.runtime_deps]
                for child in children:
                    if child not in queued:
                        queued.add(child)
                        work.append((child, path + (child,)))
            return reached if ok else None

        host_starts: dict[Ident, tuple[Ident, ...]] = {}
        for root in roots:
            reached = walk(root)
            if reached is None:
                continue
            runtime |= reached
            build |= reached
            for ident in sorted(reached):
                if ident.platform == HOST_PLATFORM:
                    continue
                meta = self.get_metadata(ident)
                if meta is None:
                    continue
                for dep in meta.build_deps:
                    host_id = Ident(dep.name, dep.version, HOST_PLATFORM)
                    host_starts.setdefault(host_id, (root, host_id))
        for host_id, host_path in sorted(host_starts.items()):
            reached = walk(host_id, host_scope=True, start_path=host_path)
            if reached is not None:
                build |= reached
        gaps.sort(key=lambda g: (g.kind, g.ident.key, tuple(p.key for p in g.path)))
        return runtime, build, gaps

    # --------------------------------------------------------------- import
    def prepare_import(self, ident: Ident, archive: bytes,
                       declared_digest: str | None = None,
                       declared_length: int | None = None,
                       runtime_deps=(), build_deps=()) -> tuple[Metadata, bytes]:
        """Verify declared digest/length *before* any durable effect."""
        actual = hashlib.sha256(archive).hexdigest()
        length = declared_length if declared_length is not None else len(archive)
        digest = declared_digest or actual
        if digest != actual:
            raise RepoError(
                f"declared digest mismatch for {ident}: declared {digest[:12]}, "
                f"actual {actual[:12]}")
        if length != len(archive):
            raise RepoError(f"declared length mismatch for {ident}: declared {length}")
        meta = Metadata(ident, digest, length, tuple(runtime_deps), tuple(build_deps))
        return meta, archive

    def begin_import(self, meta: Metadata) -> str:
        """LP of import intent: durable IMPORT_BEGIN. Returns the import id."""
        with self.lock:
            existing = self.get_metadata(meta.ident)
            if existing is not None:
                if existing != meta:
                    raise ImmutabilityViolation(
                        f"{meta.ident} already imported with different metadata")
                return ""
            import_id = uuid.uuid4().hex
            self.bus.fire("import.begin.journal")
            self._append({"type": T_IMPORT_BEGIN, "import_id": import_id,
                          "ident": meta.ident.__dict__, "meta": meta.to_dict()})
            self._imports[import_id] = _ImportIntent(import_id, meta.ident, meta)
            self.bus.fire("import.begin.durable")
            return import_id

    def stage_import(self, import_id: str, archive: bytes) -> None:
        with self.lock:
            intent = self._imports[import_id]
            self._stage_object(import_id, intent.meta.archive_digest,
                               intent.meta.archive_length, archive)
            self.bus.fire("import.object.published")

    def commit_import(self, import_id: str) -> Metadata:
        """LP of package visibility: durable IMPORT_COMMIT (meta already written)."""
        with self.lock:
            intent = self._imports[import_id]
            meta = intent.meta
            self._commit_import(import_id, meta)
            return meta

    def import_package(self, ident: Ident, archive: bytes,
                       declared_digest: str | None = None,
                       declared_length: int | None = None,
                       runtime_deps=(), build_deps=()) -> Metadata:
        meta, archive = self.prepare_import(
            ident, archive, declared_digest, declared_length, runtime_deps, build_deps)
        existing = self.get_metadata(ident)
        if existing is not None:
            if existing != meta:
                raise ImmutabilityViolation(f"{ident} already imported with different metadata")
            return existing
        import_id = self.begin_import(meta)
        self.stage_import(import_id, archive)
        return self.commit_import(import_id)

    def _stage_object(self, import_id, digest, length, archive):
        """Publish a SHA-256 addressed blob (same-digest content is shared)."""
        final = self.object_path(digest)
        if self.has_object(digest, length):
            self._imports[import_id].staged = True
            return
        os.makedirs(os.path.dirname(final), exist_ok=True)
        staged = os.path.join(self.stage_dir, f"{import_id}-{digest[:12]}")
        with open(staged, "wb") as fh:
            fh.write(archive)
            fh.flush()
            os.fsync(fh.fileno())
        actual, actual_len = _sha256_file(staged)
        if actual != digest or actual_len != length:
            os.unlink(staged)
            raise RepoError(f"on-disk verification failed for {digest[:12]}")
        try:
            os.link(staged, final)
        except FileExistsError:
            pass
        except OSError:
            os.replace(staged, final)
        else:
            os.unlink(staged)
        fsync_dir(os.path.dirname(final))
        self._imports[import_id].staged = True

    def _commit_import(self, import_id, meta: Metadata) -> Metadata:
        ident = meta.ident
        final_meta = self.meta_path(ident)
        # LP of import visibility: durable IMPORT_COMMIT first. The metadata
        # file is a derived, idempotently recreated effect, so a crash after
        # this append simply redelivers the file during recovery.
        self.bus.fire("import.commit.journal")
        self._append({"type": T_IMPORT_COMMIT, "import_id": import_id,
                      "ident": ident.__dict__, "meta": meta.to_dict()})
        self._committed_idents.add(ident)
        self._imports.pop(import_id, None)
        if not os.path.exists(final_meta):
            staged_meta = os.path.join(self.stage_dir, f"meta-{import_id}")
            atomic_write(staged_meta, canonical_json(meta.to_dict()), tmp_dir=self.stage_dir)
            os.makedirs(os.path.dirname(final_meta), exist_ok=True)
            try:
                os.link(staged_meta, final_meta)
                os.unlink(staged_meta)
            except FileExistsError:
                os.unlink(staged_meta)
                with open(final_meta, "rb") as fh:
                    existing = Metadata.from_dict(json.loads(fh.read()))
                if existing != meta:
                    raise ImmutabilityViolation(f"{ident} metadata already exists differently")
            fsync_dir(os.path.dirname(final_meta))
        self.bus.fire("import.visible")
        return meta

    def abort_import(self, import_id: str) -> None:
        with self.lock:
            self._append({"type": T_IMPORT_ABORT, "import_id": import_id})
            self._imports.pop(import_id, None)

    # -------------------------------------------------------------- snapshot
    @staticmethod
    def _snapshot_id(roots, runtime, build) -> str:
        payload = canonical_json({
            "roots": [r.__dict__ for r in sorted(roots)],
            "runtime": sorted(r.key for r in runtime),
            "build": sorted(b.key for b in build),
        })
        return hashlib.sha256(payload).hexdigest()

    def _manifest_doc(self, roots, runtime, build):
        metadata = {}
        objects: set[str] = set()
        for ident in sorted(runtime | build):
            meta = self.get_metadata(ident)
            metadata[ident.key] = meta.to_dict()
            objects.add(meta.archive_digest)
        body = {
            "roots": [r.__dict__ for r in sorted(roots)],
            "runtime": sorted(r.key for r in runtime),
            "build": sorted(b.key for b in build),
            "metadata": metadata,
            "objects": sorted(objects),
        }
        body["manifest_digest"] = hashlib.sha256(canonical_json(body)).hexdigest()
        return body

    def begin_publish(self, roots) -> str:
        """Validate both closures; LP of publish intent is durable SNAP_BEGIN.

        Blocks while a GC generation is between its scan and its done record,
        so a post-mark begin cannot observe a generation that will sweep its
        objects.
        """
        roots = tuple(roots)
        with self.lock:
            self.bus.fire("snap.wait-gate")
            while self._active_gcs:
                self._idle.wait(timeout=None)
            runtime, build, gaps = self._resolve_closure(roots)
            if gaps:
                raise IncompleteSnapshot(gaps)
            doc = self._manifest_doc(roots, runtime, build)
            snap_id = self._snapshot_id(roots, runtime, build)
            if os.path.exists(self.snap_path(snap_id)) or snap_id in self._snaps:
                return snap_id  # identical content; idempotent
            self.bus.fire("snap.begin.journal")
            rec = self._append({
                "type": T_SNAP_BEGIN, "snap_id": snap_id,
                "roots": doc["roots"], "objects": doc["objects"],
                "metadata": list(doc["metadata"].values()),
            })
            self._snaps[snap_id] = _SnapIntent(snap_id, tuple(roots),
                                               tuple(doc["objects"]), seq=rec.seq)
            self.bus.fire("snap.begin.durable")
            return snap_id

    def write_manifest(self, snap_id: str) -> None:
        with self.lock:
            intent = self._snaps[snap_id]
            if intent.manifest_written or os.path.exists(self.snap_path(snap_id)):
                intent.manifest_written = True
                return
            runtime_set, build_set, gaps = self._resolve_closure(intent.roots)
            if gaps:
                raise IncompleteSnapshot(gaps)
            doc = self._manifest_doc(intent.roots, runtime_set, build_set)
            self.bus.fire("snap.manifest.write")
            atomic_write(self.snap_path(snap_id), canonical_json(doc), tmp_dir=self.tmp_dir)
            intent.manifest_written = True
            self.bus.fire("snap.manifest.durable")

    def commit_publish(self, snap_id: str) -> str:
        """LP of publication: atomic replacement of current.json."""
        with self.lock:
            intent = self._snaps.get(snap_id)
            if intent is None:
                # Recovery may already have published this snapshot (crash
                # after the current.json replacement, before/after COMMIT):
                # the atomic replacement is the LP, so it stays published.
                if os.path.exists(self.snap_path(snap_id)):
                    current = self.current_snapshot()
                    if current == snap_id:
                        self.bus.fire("snap.published")
                        return snap_id
                raise RepoError(f"snapshot {snap_id} has no live publish intent")
            if not os.path.exists(self.snap_path(snap_id)):
                self.write_manifest(snap_id)
            self.bus.fire("snap.current.replace")
            atomic_write(self.current_path,
                         canonical_json({"snapshot": snap_id, "seq": intent.seq}),
                         tmp_dir=self.tmp_dir)
            self.bus.fire("snap.current.replaced")
            self._append({"type": T_SNAP_COMMIT, "snap_id": snap_id})
            self._snaps.pop(snap_id, None)
            self.bus.fire("snap.published")
            return snap_id

    def abort_publish(self, snap_id: str) -> None:
        with self.lock:
            self._append({"type": T_SNAP_ABORT, "snap_id": snap_id})
            self._snaps.pop(snap_id, None)
            path = self.snap_path(snap_id)
            if os.path.exists(path):
                os.unlink(path)

    def publish(self, roots) -> str:
        snap_id = self.begin_publish(roots)
        if snap_id in self._snaps:
            self.write_manifest(snap_id)
        elif not os.path.exists(self.snap_path(snap_id)):
            raise RepoError(f"snapshot {snap_id} vanished before commit")
        return self.commit_publish(snap_id)

    # ----------------------------------------------------------------- lease
    def grant_lease(self, snap_id: str | None = None, holder: str = "reader") -> str:
        """LP of a lease: durable LEASE_GRANT then the lease file exists."""
        with self.lock:
            if snap_id is None:
                snap_id = self.current_snapshot()
            if snap_id is None or not os.path.exists(self.snap_path(snap_id)):
                raise LeaseInvalid("no published snapshot to lease")
            lease_id = uuid.uuid4().hex
            self.bus.fire("lease.grant.journal")
            self._append({"type": T_LEASE_GRANT, "lease_id": lease_id,
                          "snapshot": snap_id, "holder": holder})
            self.bus.fire("lease.grant.file")
            self._write_lease_file(lease_id, snap_id, holder)
            self._leases[lease_id] = {"snapshot": snap_id, "holder": holder}
            self.bus.fire("lease.granted")
            return lease_id

    def release_lease(self, lease_id: str) -> None:
        """LP of termination: durable LEASE_RELEASE; file removal follows."""
        with self.lock:
            if lease_id not in self._leases:
                raise LeaseInvalid(f"unknown or released lease {lease_id}")
            snap_id = self._leases[lease_id]["snapshot"]
            self.bus.fire("lease.release.journal")
            self._append({"type": T_LEASE_RELEASE, "lease_id": lease_id,
                          "snapshot": snap_id})
            path = self.lease_path(lease_id)
            if os.path.exists(path):
                os.unlink(path)
                fsync_dir(self.leases_dir)
            self._leases.pop(lease_id, None)
            self.bus.fire("lease.released")

    def reap_leases(self, holder: str) -> list[str]:
        """Explicit recovery protocol: terminate every lease of ``holder``.

        Wall-clock expiry is intentionally absent; a crashed holder must be
        declared dead out-of-band, after which its leases are reaped here.
        """
        with self.lock:
            victims = [lid for lid, info in self._leases.items()
                       if info["holder"] == holder]
            for lease_id in victims:
                snap_id = self._leases[lease_id]["snapshot"]
                self._append({"type": T_LEASE_RELEASE, "lease_id": lease_id,
                              "snapshot": snap_id, "reaped": True})
                path = self.lease_path(lease_id)
                if os.path.exists(path):
                    os.unlink(path)
                self._leases.pop(lease_id, None)
            if victims:
                fsync_dir(self.leases_dir)
            return victims

    def lease_snapshot(self, lease_id: str) -> str:
        with self.lock:
            if lease_id not in self._leases:
                raise LeaseInvalid(f"invalid lease {lease_id}")
            return self._leases[lease_id]["snapshot"]

    def valid_leases(self) -> dict[str, dict]:
        with self.lock:
            return dict(self._leases)

    def read_archive(self, lease_id: str, ident: Ident) -> bytes:
        """Read through a lease; re-verifies digest and declared length."""
        snap_id = self.lease_snapshot(lease_id)
        with open(self.snap_path(snap_id), "rb") as fh:
            doc = json.loads(fh.read())
        key = ident.key
        if key not in doc["metadata"]:
            raise LeaseInvalid(f"{ident} is not part of leased snapshot {snap_id[:12]}")
        entry = doc["metadata"][key]
        path = self.object_path(entry["archive_digest"])
        with open(path, "rb") as fh:
            data = fh.read()
        digest = hashlib.sha256(data).hexdigest()
        if digest != entry["archive_digest"] or len(data) != entry["archive_length"]:
            raise RepoError(f"object {digest[:12]} failed verification during leased read")
        return data

    # -------------------------------------------------------------------- gc
    def _enumerate_objects_locked(self):
        out = []
        for dirpath, _dirs, files in os.walk(self.objects_dir):
            for name in files:
                if len(name) == 64:
                    out.append(name)
        return sorted(out)

    def _enumerate_snap_files_locked(self):
        return sorted(f[:-5] for f in os.listdir(self.snaps_dir) if f.endswith(".json"))

    def _live_roots_locked(self):
        """Snapshot ids retained at this instant (current + leased + in-flight)."""
        roots: set[str] = set()
        current = self.current_snapshot()
        if current:
            roots.add(current)
        roots.update(info["snapshot"] for info in self._leases.values())
        roots.update(self._snaps.keys())
        return {r for r in roots if os.path.exists(self.snap_path(r))}

    def _live_object_set_locked(self):
        """Object digests reachable from retained snapshots or in-flight work."""
        digests: set[str] = set()
        for snap_id in self._live_roots_locked():
            with open(self.snap_path(snap_id), "rb") as fh:
                doc = json.loads(fh.read())
            digests.update(doc["objects"])
        for intent in self._imports.values():
            if intent.staged:
                digests.add(intent.meta.archive_digest)
        for intent in self._snaps.values():
            digests.update(intent.objects)
        return digests

    def scan_generation(self) -> int:
        """LP of GC: fenced enumeration + durable GC_BEGIN in one critical section."""
        with self.lock:
            generation = 1 + max(
                (r.payload.get("generation", 0)
                 for r in self._journal.records()
                 if r.payload.get("type") == T_GC_BEGIN), default=0)
            objects = self._enumerate_objects_locked()
            manifests = self._enumerate_snap_files_locked()
            self.bus.fire("gc.begin.journal")
            self._append({"type": T_GC_BEGIN, "generation": generation,
                          "candidate_objects": objects,
                          "candidate_manifests": manifests,
                          "retained_manifests": sorted(self._live_roots_locked()),
                          "pinned_objects": sorted(self._live_object_set_locked())})
            self._active_gcs.add(generation)
            self.bus.fire("gc.begin.durable")
            return generation

    def mark_generation(self, generation: int) -> tuple[list[str], list[str]]:
        """Compute the drop lists using the fenced candidates, re-fencing pins."""
        with self.lock:
            begin = self._gc_payload(generation, T_GC_BEGIN)
            # Roots are fenced at the scan LP: anything live at that instant
            # survives the whole generation. Post-scan imports/snapshots only
            # add roots, so union with the current live set.
            pinned = set(begin["pinned_objects"]) | self._live_object_set_locked()
            drop_objects = [d for d in begin["candidate_objects"] if d not in pinned]
            retained = [d for d in begin["candidate_objects"] if d in pinned]
            live_manifests = set(begin["retained_manifests"]) | self._live_roots_locked()
            drop_manifests = [m for m in begin["candidate_manifests"]
                              if m not in live_manifests]
            self.bus.fire("gc.mark.durable")
            self._append({"type": "GC_MARK", "generation": generation,
                          "drop_objects": drop_objects,
                          "drop_manifests": drop_manifests,
                          "retained_objects": retained})
            return drop_objects, drop_manifests

    def sweep_generation(self, generation: int) -> GcReport:
        with self.lock:
            begin = self._gc_payload(generation, T_GC_BEGIN)
            mark = self._gc_payload(generation, "GC_MARK")
            report = GcReport(
                generation=generation,
                scanned_objects=len(begin["candidate_objects"]),
                scanned_manifests=len(begin["candidate_manifests"]),
                retained_objects=len(mark["retained_objects"]),
            )
            self.bus.fire("gc.sweep.manifests")
            for snap_id in mark["drop_manifests"]:
                if snap_id in self._live_roots_locked():
                    continue  # became leased/current after the mark
                path = self.snap_path(snap_id)
                if os.path.exists(path):
                    self.bus.fire("gc.unlink.manifest.before")
                    self._append({"type": T_GC_UNLINK, "generation": generation,
                                  "kind": "manifest", "id": snap_id})
                    os.unlink(path)
                    fsync_dir(self.snaps_dir)
                    report.dropped_manifests.append(snap_id)
                    self.bus.fire("gc.unlink.manifest.after")
            self.bus.fire("gc.sweep.objects")
            for digest in mark["drop_objects"]:
                # Final re-fence: never delete an object needed by a live root,
                # an in-flight snapshot, or a post-scan in-flight import.
                if digest in self._live_object_set_locked():
                    continue
                path = self.object_path(digest)
                if os.path.exists(path):
                    self.bus.fire("gc.unlink.object.before")
                    self._append({"type": T_GC_UNLINK, "generation": generation,
                                  "kind": "object", "id": digest})
                    os.unlink(path)
                    fsync_dir(os.path.dirname(path))
                    report.dropped_objects.append(digest)
                    self.bus.fire("gc.unlink.object.after")
            self.bus.fire("gc.done.journal")
            self._append({"type": T_GC_DONE, "generation": generation})
            self._active_gcs.discard(generation)
            self._idle.notify_all()
            self.bus.fire("gc.done")
            return report

    def run_gc(self) -> GcReport:
        """Full mark-sweep generation. Snapshots published after this call's
        scan are fenced out by ``begin_publish`` and survive untouched."""
        generation = self.scan_generation()
        self.mark_generation(generation)
        return self.sweep_generation(generation)

    def _gc_payload(self, generation: int, kind: str) -> dict:
        found = None
        for rec in self._journal.records():
            p = rec.payload
            if p.get("type") == kind and p.get("generation") == generation:
                found = p
        if found is None:
            raise RepoError(f"missing {kind} record for generation {generation}")
        return found

    def _resume_gc(self, generation: int) -> None:
        """Finish a generation that crashed anywhere in scan/mark/sweep."""
        try:
            self._gc_payload(generation, T_GC_BEGIN)
        except RepoError:
            self._active_gcs.discard(generation)
            return
        try:
            self._gc_payload(generation, "GC_MARK")
        except RepoError:
            self.mark_generation(generation)
        report = self.sweep_generation(generation)
        self._gc_resume_report = report
