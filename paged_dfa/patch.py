"""Reversible per-page patching and hot-swap versioning.

A patch edits individual cells of an immutable image.  Sharing is preserved by
copy-on-write: an edited state receives a private alias whose base is the row
it previously resolved to, and only pages covering edited symbols are rebuilt.
The delta records old/new blobs per changed page plus the old/new version
regions, so it can be re-applied and inverted byte-for-byte while the old view
keeps operating on the old bytes it references.
"""

from dataclasses import dataclass

from .codec import (
    Alias,
    EncodedImage,
    clip_intervals,
    encode_image,
    page_count_for,
    parse_index,
)
from .errors import IntegrityError, InvalidPage
from .model import DEAD
from .wire import Reader, Writer, digest


@dataclass(frozen=True)
class Edit:
    state: int
    symbol: int
    target: int  # state id, or DEAD for an explicit dead transition


@dataclass(frozen=True)
class PageDelta:
    pid: int
    old_blob: bytes
    new_blob: bytes


@dataclass(frozen=True)
class Delta:
    old_tag: bytes
    new_tag: bytes
    old_header2: bytes
    new_header2: bytes
    page_deltas: tuple[PageDelta, ...]
    edits: tuple[Edit, ...]

    @property
    def changed_pages(self) -> frozenset[int]:
        return frozenset(d.pid for d in self.page_deltas)

    def serialize(self) -> bytes:
        w = Writer().put(self.old_tag).put(self.new_tag)
        w.u(len(self.old_header2)).put(self.old_header2)
        w.u(len(self.new_header2)).put(self.new_header2)
        w.u(len(self.page_deltas))
        for d in self.page_deltas:
            w.u(d.pid).u(len(d.old_blob)).put(d.old_blob)
            w.u(len(d.new_blob)).put(d.new_blob)
        w.u(len(self.edits))
        for e in self.edits:
            w.u(e.state).s(e.symbol).s(e.target)
        body = w.finish()
        return body + digest(body)

    @classmethod
    def deserialize(cls, data) -> "Delta":
        data = bytes(data)
        body, check = data[:-16], data[-16:]
        if digest(body) != check:
            raise IntegrityError("delta checksum mismatch")
        r = Reader(body)
        old_tag, new_tag = r.bytes(8), r.bytes(8)
        old_h2 = r.bytes(r.uv())
        new_h2 = r.bytes(r.uv())
        pages = []
        for _ in range(r.uv()):
            pid = r.uv()
            old_blob = r.bytes(r.uv())
            new_blob = r.bytes(r.uv())
            pages.append(PageDelta(pid, old_blob, new_blob))
        edits = tuple(Edit(r.uv(), r.sv(), r.sv()) for _ in range(r.uv()))
        return cls(old_tag, new_tag, old_h2, new_h2, tuple(pages), edits)


@dataclass(frozen=True)
class PatchResult:
    image: EncodedImage
    delta: Delta
    pages_read: tuple[int, ...]


# ---------------------------------------------------------------------------
# Header2 (version region) serialization
# ---------------------------------------------------------------------------


def _serialize_header2(tag, aliases, state_count):
    w = Writer().put(tag)
    if aliases:
        w.u(len(aliases))
        for alias in aliases:
            if alias is None:
                w.u(0)
            else:
                w.u(alias.base + 1)
                w.u(len(alias.override_pages))
                for pid in sorted(alias.override_pages):
                    w.u(pid)
    else:
        w.u(0)
    return w.finish()


def parse_header2(data, page_count):
    r = Reader(bytes(data))
    tag = r.bytes(8)
    count = r.uv()
    aliases = []
    for _ in range(count):
        raw = r.uv()
        if raw == 0:
            aliases.append(None)
        else:
            pages = frozenset(r.uv() for _ in range(r.uv()))
            aliases.append(Alias(raw - 1, pages))
    if r.pos != len(data):
        raise IntegrityError("trailing bytes in version region")
    return tag, tuple(aliases)


# ---------------------------------------------------------------------------
# Logical resolution of one state's effective exceptions
# ---------------------------------------------------------------------------


def _state_global_intervals(idx, state, page_map, needed_pages):
    """Effective global exceptions for a state.

    Only pages in ``needed_pages`` (the state's existing overrides that are not
    about to be rebuilt anyway are not required; callers pass exactly the pages
    they cannot derive from the index) are fetched.  Concrete states need none.
    """
    from .codec import clip_intervals

    rid = idx.row_of[state]
    alias = idx.aliases[state] if idx.aliases else None
    if alias is None:
        return rid, idx.rows[rid].default, idx.rows[rid].exceptions
    base = alias.base
    intervals = []
    for pid in range(idx.page_count):
        lo = pid * idx.page_size
        if pid in needed_pages and pid in alias.override_pages:
            page = page_map(pid)
            frag = page.get(idx.row_count + state, ())
        else:
            frag = clip_intervals(idx.rows[base].exceptions, pid,
                                  idx.page_size, idx.symbol_count)
        intervals.extend((lo + a, b, c) for a, b, c in frag)
    return base, idx.rows[base].default, tuple(intervals)


def _reemit(intervals, pos_values, default, symbol_count):
    """Apply ``pos_values`` edits to global intervals, keeping ``default``."""
    values = {}
    for s_, length, target in intervals:
        for p in range(s_, s_ + length):
            values[p] = target
    values.update(pos_values)
    out = []
    run_start = run_target = None
    for p in range(symbol_count):
        v = values.get(p, default)
        if v == default:
            if run_start is not None:
                out.append((run_start, p - run_start, run_target))
                run_start = run_target = None
        elif run_start is None:
            run_start, run_target = p, v
        elif v != run_target:
            out.append((run_start, p - run_start, run_target))
            run_start, run_target = p, v
    if run_start is not None:
        out.append((run_start, symbol_count - run_start, run_target))
    return tuple(out)


# ---------------------------------------------------------------------------
# Page blob building (kept local so patch.py can rebuild one page)
# ---------------------------------------------------------------------------


def _build_page_blob(pid, entries, salt, page_size, symbol_count, state_count):
    from .codec import _build_page_blob as _build
    return _build(pid, entries, salt)


def _changed_page_entries(idx, pid, overrides):
    """Concrete fragments for the page plus every alias override."""
    from .codec import clip_intervals

    out = []
    for rid, row in enumerate(idx.rows):
        frag = clip_intervals(row.exceptions, pid, idx.page_size, idx.symbol_count)
        if frag:
            out.append((rid, frag))
    for state, intervals in sorted(overrides.items()):
        frag = clip_intervals(intervals, pid, idx.page_size, idx.symbol_count)
        out.append((idx.row_count + state, frag))
    return tuple(out)


def build_patch(old_blob, edits, *, fetcher=None, new_tag=None):
    """Build a new image and a reversible delta from a small set of edits.

    Only pages covering edited symbols are fetched/validated; every other page
    region is copied verbatim from ``old_blob``.  ``pages_read`` lists the pages
    actually decoded, which for local edits is exactly the touched page set.
    """
    import os
    from .codec import parse_page

    old_blob = bytes(old_blob)
    idx = parse_index(old_blob)
    new_tag = os.urandom(8) if new_tag is None else new_tag
    if len(new_tag) != 8:
        raise ValueError("new tag must be 8 bytes")
    symbols = idx.alphabet
    valid_targets = set(range(idx.state_count)) | {DEAD}

    grouped: dict[int, dict[int, int]] = {}
    for e in edits:
        if not (0 <= e.state < idx.state_count):
            raise ValueError(f"edit targets unknown state {e.state}")
        if e.symbol not in symbols:
            raise ValueError(f"edit symbol {e.symbol} not in alphabet")
        if e.target not in valid_targets:
            raise ValueError(f"edit target {e.target} is not a state or DEAD")
        pos = symbols.index(e.symbol)
        grouped.setdefault(e.state, {})[pos] = e.target

    parsed_pages = {}

    def page_map(pid):
        if pid not in parsed_pages:
            entry = idx.toc[pid]
            if fetcher is None:
                raw = old_blob[entry.offset:entry.offset + entry.length]
            else:
                raw = fetcher(pid, entry.offset, entry.length)
            parsed_pages[pid] = parse_page(raw, pid, idx)
        return parsed_pages[pid]

    aliases = list(idx.aliases) if idx.aliases else [None] * idx.state_count

    def effective_value(state, pos):
        rid = idx.row_of[state]
        pid = pos // idx.page_size
        local = pos - pid * idx.page_size
        alias = aliases[state]
        if alias is None:
            frag = clip_intervals(idx.rows[rid].exceptions, pid,
                                  idx.page_size, idx.symbol_count)
            default = idx.rows[rid].default
        elif pid in alias.override_pages:
            page = page_map(pid)
            frag = page.get(idx.row_count + state, ())
            default = idx.rows[alias.base].default
        else:
            frag = clip_intervals(idx.rows[alias.base].exceptions, pid,
                                  idx.page_size, idx.symbol_count)
            default = idx.rows[alias.base].default
        for a, b, target in frag:
            if a <= local < a + b:
                return target
        return default

    # Drop edits that leave the effective cell unchanged (this also collapses
    # "set dead to dead" and shared-row identity writes).
    filtered: dict[int, dict[int, int]] = {}
    for state, cells in grouped.items():
        for pos, target in cells.items():
            if effective_value(state, pos) != target:
                filtered.setdefault(state, {})[pos] = target
    if not filtered:
        parsed_pages.clear()
        old_h2 = _extract_old_header2(old_blob, idx)
        delta = Delta(idx.tag, idx.tag, old_h2, old_h2, (), tuple(edits))
        return PatchResult(
            EncodedImage(old_blob, idx), delta, ()
        )
    grouped = filtered
    touched_pages = {pos // idx.page_size
                     for cells in grouped.values() for pos in cells}


    # Pages an edited state already overrides in the old image, restricted to
    # pages this patch touches: those are the only old pages we must read.
    edited_pages: dict[int, set[int]] = {
        state: {pos // idx.page_size for pos in cells}
        for state, cells in grouped.items()
    }
    needed: dict[int, set[int]] = {}
    for state, pages in edited_pages.items():
        alias = aliases[state]
        old_overrides = set(alias.override_pages) if alias is not None else set()
        # Read an old override when the state edits that page itself, or when
        # another state's edit forces the page to be rebuilt (the surviving
        # override is needed to rebuild this state's global intervals).
        needed[state] = old_overrides & touched_pages

    state_intervals: dict[int, tuple] = {}
    state_base: dict[int, int] = {}
    for state in grouped:
        base, _default, intervals = _state_global_intervals(
            idx, state, page_map, needed[state]
        )
        state_intervals[state] = intervals
        state_base[state] = base

    new_aliases = list(aliases)
    override_pages: dict[int, set[int]] = {}
    for state in grouped:
        alias = new_aliases[state]
        # Preserve every previously overridden page (carried on unchanged page
        # blobs); a new edit only adds its touched pages to the set.
        pages = set(alias.override_pages) if alias is not None else set()
        pages.update(edited_pages[state])
        override_pages[state] = pages
        new_aliases[state] = Alias(state_base[state], frozenset(pages))

    new_intervals: dict[int, tuple] = {}
    for state, cells in grouped.items():
        default = idx.rows[state_base[state]].default
        new_intervals[state] = _reemit(
            state_intervals[state], cells, default, idx.symbol_count
        )

    # All overrides that must appear on each rebuilt page: edited states plus
    # pre-existing overrides of other states already present on that page.
    all_overrides: dict[int, dict[int, tuple]] = {}
    for state, pages in override_pages.items():
        for pid in pages:
            all_overrides.setdefault(pid, {})[state] = new_intervals[state]
    for pid in touched_pages:
        page = page_map(pid)  # read the old page once
        for effective, frag in page.items():
            if effective >= idx.row_count:
                other = effective - idx.row_count
                all_overrides.setdefault(pid, {}).setdefault(
                    other, _frag_to_global(frag, pid, idx)
                )

    salt = idx.page_salt
    new_blobs = {}
    page_deltas = []
    for pid in touched_pages:
        entries = _changed_page_entries(idx, pid, all_overrides[pid])
        new_blob = _build_page_blob(
            pid, entries, salt, idx.page_size, idx.symbol_count, idx.state_count
        )
        entry = idx.toc[pid]
        old_page = old_blob[entry.offset:entry.offset + entry.length]
        new_blobs[pid] = new_blob
        if new_blob != old_page:
            page_deltas.append(PageDelta(pid, old_page, new_blob))

    new_h2 = _serialize_header2(new_tag, tuple(new_aliases), idx.state_count)
    # Rebuild the whole image reusing untouched page bytes.
    model = idx.as_model()
    page_blobs = {
        pid: (new_blobs[pid] if pid in new_blobs
              else old_blob[idx.toc[pid].offset:idx.toc[pid].offset + idx.toc[pid].length])
        for pid in range(idx.page_count)
    }
    new_image_blob = encode_image(
        model,
        page_size=idx.page_size,
        tag=new_tag,
        aliases=tuple(new_aliases),
        override_fragments={},
        page_blobs=page_blobs,
    )
    # encode_image ignores override_fragments when page_blobs is given; ensure
    # the rebuilt pages are actually embedded (they are, via page_blobs).
    old_h2 = _extract_old_header2(old_blob, idx)
    delta = Delta(
        idx.tag, new_tag, old_h2, new_h2,
        tuple(sorted(page_deltas, key=lambda d: d.pid)),
        tuple(edits),
    )
    return PatchResult(
        EncodedImage(new_image_blob, parse_index(new_image_blob)),
        delta,
        tuple(sorted(parsed_pages)),
    )


def _frag_to_global(frag, pid, idx):
    lo = pid * idx.page_size
    return tuple((lo + a, b, c) for a, b, c in frag)


def _extract_old_header2(blob, idx):
    # h1 length is recoverable: h2 starts immediately after h1 and ends at TOC.
    # We find h1 end as the position where salt bytes would begin; the encoder
    # tracked nothing, so recompute by scanning via a fresh parse marker.
    return _header2_slice(blob, idx)[0]


def _header2_slice(blob, idx):
    """Return (h2_bytes, h2_start, h2_end) using structural knowledge."""
    # pages_start is end-of-index; TOC + digest precede it.  Walk backwards:
    # the TOC varint size depends only on pc and offsets, so reparse from the
    # known h1 boundary instead.  h1 boundary: parse_index computed salt over
    # h1; store its length on the index lazily by re-parsing prefix.
    start = _h1_end(blob, idx)
    # h2 = tag(8) + alias vector; alias vector length is known from idx.
    end = _skip_header2(blob, start)
    return bytes(blob[start:end]), start, end


def _h1_end(blob, idx):
    from .wire import MAGIC, FORMAT_VERSION

    r = Reader(bytes(blob))
    r.bytes(4)
    r.uv()         # format version
    r.uv()         # page size (lives in the salt base)
    symbol_count = r.uv()
    for _ in range(symbol_count):
        r.sv()
    state_count = r.uv()
    accept_count = r.uv()
    for _ in range(accept_count):
        r.uv()
    row_count = r.uv()
    for _ in range(row_count):
        r.sv()
        for _ in range(r.uv()):
            r.uv(); r.uv(); r.sv()
    mapped = r.uv()
    for _ in range(mapped):
        r.uv()
    return r.pos


def _skip_header2(blob, start):
    r = Reader(bytes(blob), start + 8)
    count = r.uv()
    for _ in range(count):
        raw = r.uv()
        if raw != 0:
            for _ in range(r.uv()):
                r.uv()
    return r.pos


def apply_delta(old_blob, delta: Delta, *, forward: bool):
    """Rebuild an image by swapping the version region and changed page blobs.

    ``forward=True`` turns old into new; ``forward=False`` inverts it.  The
    image is re-verified by the caller through :func:`parse_index`, and every
    embedded new/old page is verified against the TOC at the next page load.
    """
    old_blob = bytes(old_blob)
    idx = parse_index(old_blob)
    h2, h2_start, h2_end = _header2_slice(old_blob, idx)
    expected_h2 = delta.old_header2 if forward else delta.new_header2
    if h2 != expected_h2:
        raise IntegrityError("delta version region does not match the base image")
    wanted_tag = delta.new_tag if forward else delta.old_tag
    new_h2 = delta.new_header2 if forward else delta.old_header2
    if not new_h2.startswith(wanted_tag):
        raise IntegrityError("delta tag/region inconsistency")

    pages = {}
    for d in delta.page_deltas:
        entry = idx.toc[d.pid]
        current = old_blob[entry.offset:entry.offset + entry.length]
        expected = d.old_blob if forward else d.new_blob
        replacement = d.new_blob if forward else d.old_blob
        if current != expected:
            raise IntegrityError(f"page {d.pid} does not match the delta base")
        pages[d.pid] = replacement

    # Layout: replace h2 in-place first (length may change), then re-encode the
    # index using unchanged page regions and explicit replacement blobs.
    # Parse the target header2 to obtain aliases; rows/states come from idx.
    _tag, aliases = parse_header2(new_h2, idx.page_count)
    page_blobs = {}
    for pid in range(idx.page_count):
        if pid in pages:
            page_blobs[pid] = pages[pid]
        else:
            e = idx.toc[pid]
            page_blobs[pid] = old_blob[e.offset:e.offset + e.length]
    out = encode_image(
        idx.as_model(),
        page_size=idx.page_size,
        tag=wanted_tag,
        aliases=aliases,
        override_fragments={},
        page_blobs=page_blobs,
    )
    parse_index(out)  # refuse to return an unverifiable image
    return out


# ---------------------------------------------------------------------------
# Version store: hot swap with reference-fixed old views
# ---------------------------------------------------------------------------


class Version:
    """One immutable image under a content-derived id."""

    __slots__ = ("version_id", "blob", "tag", "index")

    def __init__(self, blob: bytes):
        from .codec import parse_index

        self.blob = bytes(blob)
        self.version_id = digest(self.blob).hex()
        self.index = parse_index(self.blob)
        self.tag = self.index.tag

    def __repr__(self):
        return f"Version({self.version_id[:12]})"


class VersionStore:
    """Holds live images; old images are retained while views pin them.

    A patched version is committed atomically: :meth:`current` changes at once,
    while every view opened earlier keeps using the bytes it was pinned to.
    Images only become eligible for removal after all their views are closed,
    so querying an old view never observes a half-swapped table.
    """

    def __init__(self, first_blob: bytes):
        self._versions: dict[str, Version] = {}
        self._pins: dict[str, int] = {}
        first = Version(first_blob)
        self._versions[first.version_id] = first
        self._current_id = first.version_id

    @property
    def current(self) -> Version:
        return self._versions[self._current_id]

    def versions(self) -> tuple[str, ...]:
        return tuple(self._versions)

    def pin(self, version_id: str):
        if version_id not in self._versions:
            raise KeyError(version_id)
        self._pins[version_id] = self._pins.get(version_id, 0) + 1

    def unpin(self, version_id: str):
        n = self._pins.get(version_id, 0)
        if n <= 1:
            self._pins.pop(version_id, None)
        else:
            self._pins[version_id] = n - 1
        self._gc()

    def open_view(self, version_id: str | None = None, *, fetcher=None):
        from .view import View, InMemoryBackend

        version = self.current if version_id is None else self._versions[version_id]
        self.pin(version.version_id)
        if fetcher is None:
            backend = InMemoryBackend(version.blob)
            fetcher = backend.fetch
        view = View(version.blob, fetcher=fetcher)
        view._store = self
        view._version_id = version.version_id
        original_close = view.close

        def close():
            original_close()
            self.unpin(version.version_id)

        view.close = close
        return view

    def commit(self, result: PatchResult, *, gc: bool = True) -> Version:
        new = Version(result.image.blob)
        if new.version_id not in self._versions:
            self._versions[new.version_id] = new
        self._current_id = new.version_id
        if gc:
            self._gc()
        return new

    def commit_blob(self, blob: bytes, *, gc: bool = True) -> Version:
        new = Version(blob)
        self._versions.setdefault(new.version_id, new)
        self._current_id = new.version_id
        if gc:
            self._gc()
        return new

    def rollback_to(self, version_id: str, *, gc: bool = True):
        if version_id not in self._versions:
            raise KeyError(version_id)
        self._current_id = version_id
        if gc:
            self._gc()

    def _gc(self):
        for vid in list(self._versions):
            if vid == self._current_id or self._pins.get(vid, 0) > 0:
                continue
            del self._versions[vid]
