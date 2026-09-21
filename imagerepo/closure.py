"""Independent closure calculator.

Deliberately kept independent of :mod:`imagerepo.repo`: it only needs two
injected lookup callables, so it can audit a repository the way a separate
tool would.

A snapshot is complete only when **both** closures are complete:

* runtime closure: each root plus its transitive ``runtime_deps`` on the
  root's own platform;
* host/build closure: every ``build_dep`` edge moves to the ``host``
  platform; host packages then contribute both their runtime and build deps
  (still on host). The host closure also includes the runtime closure,
  because a build needs the artifacts being built as well.

Missing items are classified as:

* ``metadata`` — no metadata record for the identity;
* ``content``  — metadata exists but its archive blob is absent;
* ``digest``   — the blob exists but digest/length is wrong.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .model import HOST_PLATFORM, Ident, Metadata

METADATA_MISSING = "metadata"
CONTENT_MISSING = "content"
DIGEST_MISMATCH = "digest"


@dataclass(frozen=True)
class Gap:
    kind: str
    ident: Ident
    path: tuple[Ident, ...]
    detail: str = ""


@dataclass
class ClosureResult:
    runtime: set[Ident] = field(default_factory=set)
    build: set[Ident] = field(default_factory=set)
    gaps: list[Gap] = field(default_factory=list)

    @property
    def ok(self) -> bool:
        return not self.gaps


def _walk(start, get_meta, has_object, check_digest, note, *, host_scope=False,
          start_path=None):
    """Fixpoint walk from ``start`` with BFS paths.

    Target scope (``host_scope=False``): only ``runtime_deps``, same platform.
    Host scope: host packages contribute both dep kinds, all on host.
    """
    reached: set[Ident] = set()
    queued = {start}
    work = [(start, start_path or (start,))]
    complete = True
    while work:
        ident, path = work.pop(0)
        if ident in reached:
            continue
        meta = get_meta(ident)
        if meta is None:
            note(METADATA_MISSING, ident, path)
            complete = False
            continue
        if not has_object(meta.archive_digest, meta.archive_length):
            note(CONTENT_MISSING, ident, path, meta.archive_digest[:12])
            complete = False
            continue
        if check_digest is not None:
            err = check_digest(meta)
            if err:
                note(DIGEST_MISMATCH, ident, path, err)
                complete = False
                continue
        reached.add(ident)
        if host_scope:
            children = [Ident(d.name, d.version, HOST_PLATFORM)
                        for d in list(meta.runtime_deps) + list(meta.build_deps)]
        else:
            children = [Ident(d.name, d.version, ident.platform)
                        for d in meta.runtime_deps]
        for child in children:
            if child not in queued:
                queued.add(child)
                work.append((child, path + (child,)))
    return reached if complete else None


def compute_closure(roots, get_meta, has_object, *, check_digest=None) -> ClosureResult:
    roots = tuple(roots)
    result = ClosureResult()
    reported: set = set()

    def note(kind, ident, path, detail=""):
        marker = (kind, ident, tuple(path))
        if marker not in reported:
            reported.add(marker)
            result.gaps.append(Gap(kind, ident, tuple(path), detail))

    # Host build roots discovered across roots, with the target edge in path.
    host_starts: dict[Ident, tuple[Ident, ...]] = {}
    for root in roots:
        reached = _walk(root, get_meta, has_object, check_digest, note)
        if reached is None:
            continue
        result.runtime |= reached
        result.build |= reached
        # Discover build_dep edges (including via a runtime dep's own metadata).
        for ident in sorted(reached):
            if ident.platform == HOST_PLATFORM:
                continue
            meta = get_meta(ident)
            if meta is None:
                continue
            for dep in meta.build_deps:
                host_id = Ident(dep.name, dep.version, HOST_PLATFORM)
                host_starts.setdefault(host_id, (root, host_id))
    for host_id, host_path in sorted(host_starts.items()):
        reached = _walk(host_id, get_meta, has_object, check_digest, note,
                        host_scope=True, start_path=host_path)
        if reached is not None:
            result.build |= reached

    result.gaps.sort(key=lambda g: (g.kind, g.ident.key, tuple(p.key for p in g.path)))
    return result
