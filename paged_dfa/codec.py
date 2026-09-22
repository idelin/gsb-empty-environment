"""Page-aware encoder and strict index/page parser.

Layout::

    [ header ][ toc ][ index_digest16 ][ page blobs ]

* ``index_digest`` = BLAKE2b(header || toc); it is verified before a View is
  handed out, so a *corrupted offset* or tampered TOC never opens.
* page salt = BLAKE2b(header).  Every page checksum is keyed by
  ``salt || page_id``: a page body cannot be moved between pages or images.
* TOC additionally records the full-blob digest of each page region.

Every page is a *full slice*: it stores the exception fragments of all rows in
that symbol range.  A patched page appends alias-override entries but keeps the
concrete fragments byte-for-byte, so untouched pages are reused verbatim.
"""

from dataclasses import dataclass

from .errors import IntegrityError, InvalidPage
from .model import DEAD, Model, Row, build_model
from .wire import FORMAT_VERSION, MAGIC, Reader, Writer, digest


@dataclass(frozen=True)
class Alias:
    base: int
    override_pages: frozenset[int]


@dataclass(frozen=True)
class TocEntry:
    offset: int
    length: int
    digest: bytes


@dataclass(frozen=True)
class Index:
    tag: bytes
    page_size: int
    alphabet: tuple[int, ...]
    accepting: frozenset[int]
    rows: tuple[Row, ...]
    row_of: tuple[int, ...]
    aliases: tuple[Alias | None, ...]
    toc: tuple[TocEntry, ...]
    pages_start: int
    page_salt: bytes

    @property
    def state_count(self) -> int:
        return len(self.row_of)

    @property
    def symbol_count(self) -> int:
        return len(self.alphabet)

    @property
    def row_count(self) -> int:
        return len(self.rows)

    @property
    def page_count(self) -> int:
        return len(self.toc)

    def as_model(self) -> Model:
        return Model(self.alphabet, self.accepting, self.rows, self.row_of)


@dataclass(frozen=True)
class EncodedImage:
    blob: bytes
    index: Index


def page_count_for(symbol_count: int, page_size: int) -> int:
    return (symbol_count + page_size - 1) // page_size


def clip_intervals(exceptions, pid: int, page_size: int, symbol_count: int):
    """Fragment of ``exceptions`` inside page ``pid``.

    A run crossing a page boundary is split, so the returned [start, start+len)
    always lies inside the page.
    """
    lo = pid * page_size
    hi = min(lo + page_size, symbol_count)
    frag = []
    for start, length, target in exceptions:
        s, e = max(start, lo), min(start + length, hi)
        if s < e:
            frag.append((s - lo, e - s, target))
    return tuple(frag)


def _build_page_blob(pid: int, entries, salt: bytes) -> bytes:
    """entries: (effective_row_id, fragment); empty fragment marks an override
    that deletes every exception on the page."""
    payload = Writer()
    payload.u(pid).u(len(entries))
    for effective_rid, frag in entries:
        payload.s(effective_rid).u(len(frag))
        for start, length, target in frag:
            payload.u(start).u(length).s(target)
    body = payload.finish()
    return body + digest(body, key=salt + pid.to_bytes(4, "little"))


def _header1(model, page_size):
    w = Writer()
    w.put(MAGIC).u(FORMAT_VERSION).u(page_size)
    w.u(model.symbol_count)
    for sym in model.alphabet:
        w.s(sym)
    w.u(model.state_count)
    w.u(len(model.accepting))
    for state in sorted(model.accepting):
        w.u(state)
    w.u(len(model.rows))
    for row in model.rows:
        w.s(row.default).u(len(row.exceptions))
        for start, length, target in row.exceptions:
            w.u(start).u(length).s(target)
    w.u(model.state_count)
    for rid in model.row_of:
        w.u(rid)
    return w.finish()


def _header2(tag, aliases, state_count):
    w = Writer().put(tag)
    if aliases:
        if len(aliases) != state_count:
            raise ValueError("alias vector must be empty or one entry per state")
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


def encode_image(
    model: Model,
    *,
    page_size: int,
    tag: bytes,
    aliases: tuple[Alias | None, ...] = (),
    override_fragments: dict[tuple[int, int], tuple] | None = None,
    page_blobs: dict[int, bytes] | None = None,
) -> bytes:
    """Serialize a model.

    The stable header (alphabet, states, rows, state->row map) is the page
    salt base; the tag and alias vector live in the version region and therefore
    change without invalidating untouched page bytes.

    ``override_fragments[(state, pid)]`` supplies an alias override;
    ``page_blobs`` may provide already-built blobs for pages (reused byte for
    byte when absent from the change set).
    """
    if page_size <= 0:
        raise ValueError("page_size must be positive")
    if len(tag) != 8:
        raise ValueError("tag must be 8 bytes")
    symbols = model.symbol_count
    state_count = model.state_count
    pc = page_count_for(symbols, page_size)
    overrides = override_fragments or {}

    h1 = _header1(model, page_size)
    h2 = _header2(tag, aliases, state_count)
    salt = digest(h1)

    override_by_page: dict[int, list[tuple[int, tuple]]] = {}
    for (state, pid), frag in overrides.items():
        if not (0 <= state < state_count) or not (0 <= pid < pc):
            raise ValueError("override key out of range")
        override_by_page.setdefault(pid, []).append((len(model.rows) + state, frag))

    if page_blobs is not None:
        if set(page_blobs) != set(range(pc)):
            raise ValueError("page_blobs must provide every page id")
        blobs = [page_blobs[p] for p in range(pc)]
    else:
        def page_entries(pid):
            out = []
            for rid, row in enumerate(model.rows):
                frag = clip_intervals(row.exceptions, pid, page_size, symbols)
                if frag:
                    out.append((rid, frag))
            out.extend(sorted(override_by_page.get(pid, ())))
            return tuple(out)

        blobs = [_build_page_blob(pid, page_entries(pid), salt) for pid in range(pc)]

    prefix_len = len(h1) + len(h2)
    section_start = prefix_len
    toc = b""
    for _ in range(8):
        w = Writer().u(pc)
        cursor = section_start
        for blob in blobs:
            w.u(cursor).u(len(blob)).put(digest(blob))
            cursor += len(blob)
        toc = w.finish()
        new_start = prefix_len + len(toc) + 16
        if new_start == section_start:
            break
        section_start = new_start
    else:
        raise RuntimeError("layout fixed point did not converge")

    out = bytearray()
    out.extend(h1)
    out.extend(h2)
    out.extend(toc)
    out.extend(digest(h1 + h2 + toc))
    out.extend(b"".join(blobs))
    return bytes(out)


class DFAEncoder:
    def __init__(self, page_size: int):
        self.page_size = page_size

    def encode(self, alphabet, table, accepting=(), *, tag: bytes | None = None):
        import os

        model = build_model(alphabet, table, accepting)
        tag = os.urandom(8) if tag is None else tag
        blob = encode_image(model, page_size=self.page_size, tag=tag)
        return EncodedImage(blob, parse_index(blob))


# ---------------------------------------------------------------------------
# Parsing / verification
# ---------------------------------------------------------------------------


def parse_index(blob) -> Index:
    """Validate the whole index (salt base + version region + TOC).

    No View is created unless this succeeds: magic/version, alphabet ordering,
    row/alias/state references, interval bounds, page offsets (overlap and
    range) and the index checksum are all checked up front.
    """
    view = bytes(blob)
    r = Reader(view)
    try:
        if r.bytes(4) != MAGIC:
            raise IntegrityError("bad magic")
        if r.uv() != FORMAT_VERSION:
            raise IntegrityError("unsupported format version")
        page_size = r.uv()
        if page_size == 0:
            raise IntegrityError("page size is zero")
        symbol_count = r.uv()
        if symbol_count == 0:
            raise IntegrityError("empty alphabet")
        alphabet = tuple(r.sv() for _ in range(symbol_count))
        if len(set(alphabet)) != symbol_count or list(alphabet) != sorted(alphabet):
            raise IntegrityError("alphabet not strictly sorted")
        state_count = r.uv()
        if state_count == 0:
            raise IntegrityError("state count is zero")
        accepting = frozenset(r.uv() for _ in range(r.uv()))
        if not all(0 <= s < state_count for s in accepting):
            raise IntegrityError("accepting state out of range")

        row_count = r.uv()
        if row_count == 0:
            raise IntegrityError("row count is zero")
        valid_targets = set(range(state_count)) | {DEAD}
        rows = []
        for _ in range(row_count):
            default = r.sv()
            if default not in valid_targets:
                raise IntegrityError("row default targets unknown state")
            intervals = []
            prev_end = -1
            for _ in range(r.uv()):
                start, length, target = r.uv(), r.uv(), r.sv()
                if length == 0 or start <= prev_end or start < 0:
                    raise IntegrityError("invalid interval order in row")
                if start + length > symbol_count:
                    raise IntegrityError("interval exceeds alphabet")
                if target not in valid_targets:
                    raise IntegrityError("interval targets unknown state")
                intervals.append((start, length, target))
                prev_end = start + length - 1
            rows.append(Row(default, tuple(intervals)))

        if r.uv() != state_count:
            raise IntegrityError("state row map length mismatch")
        row_of = tuple(r.uv() for _ in range(state_count))
        if any(not (0 <= rid < row_count) for rid in row_of):
            raise IntegrityError("state references unknown row")

        h1_end = r.pos
        salt = digest(view[:h1_end])

        tag = r.bytes(8)
        pc = page_count_for(symbol_count, page_size)
        alias_count = r.uv()
        if alias_count not in (0, state_count):
            raise IntegrityError("alias vector length mismatch")
        aliases = []
        for i in range(alias_count):
            base_raw = r.uv()
            if base_raw == 0:
                aliases.append(None)
                continue
            pages = frozenset(r.uv() for _ in range(r.uv()))
            if any(not (0 <= p < pc) for p in pages):
                raise IntegrityError(f"alias {i} overrides unknown page")
            base = base_raw - 1
            if not (0 <= base < row_count):
                raise IntegrityError(f"alias {i} has unknown base row")
            aliases.append(Alias(base, pages))

        toc_pos = r.pos
        declared_pages = r.uv()
        if declared_pages != pc:
            raise IntegrityError("page count does not match alphabet/page size")
        toc = []
        for pid in range(pc):
            offset, length = r.uv(), r.uv()
            dg = r.bytes(16)
            toc.append(TocEntry(offset, length, dg))

        blob_section = r.pos + 16
        for pid, entry in enumerate(toc):
            if entry.offset < blob_section:
                raise IntegrityError(f"page {pid} offset points inside index")
            if entry.length == 0:
                raise IntegrityError(f"page {pid} has zero length")
        order = sorted(range(pc), key=lambda i: toc[i].offset)
        for prev, pid in zip(order, order[1:]):
            if toc[prev].offset + toc[prev].length > toc[pid].offset:
                raise IntegrityError("page regions overlap")
        if toc and toc[order[-1]].offset + toc[order[-1]].length > len(view):
            raise IntegrityError("last page exceeds container")

        toc_end = r.pos
        if r.bytes(16) != digest(view[:toc_pos] + view[toc_pos:toc_end]):
            raise IntegrityError("index checksum mismatch")
        return Index(
            tag, page_size, alphabet, accepting,
            tuple(rows), row_of, tuple(aliases), tuple(toc),
            blob_section, salt,
        )
    except EOFError as exc:
        raise IntegrityError("truncated index") from exc


def parse_page(raw, pid: int, index: Index) -> dict[int, tuple]:
    """Validate one page and return ``{effective_row_id: fragment}``.

    The TOC digest, page checksum, embedded page id, interval bounds and every
    destination state are verified before the fragment becomes visible.
    """
    raw = bytes(raw)
    entry = index.toc[pid]
    if len(raw) != entry.length:
        raise InvalidPage(f"page {pid} length disagrees with TOC")
    if digest(raw) != entry.digest:
        raise InvalidPage(f"page {pid} fails TOC digest")
    body, stored = raw[:-16], raw[-16:]
    if stored != digest(body, key=index.page_salt + pid.to_bytes(4, "little")):
        raise InvalidPage(f"page {pid} checksum mismatch")

    r = Reader(body)
    try:
        if r.uv() != pid:
            raise InvalidPage(f"page {pid} embeds a different id")
        count = r.uv()
        lo = pid * index.page_size
        hi = min(lo + index.page_size, len(index.alphabet))
        valid_targets = set(range(index.state_count)) | {DEAD}
        limit = hi - lo
        result = {}
        for _ in range(count):
            effective = r.sv()
            n = r.uv()
            frag = []
            prev_end = -1
            for _ in range(n):
                start, length, target = r.uv(), r.uv(), r.sv()
                if length == 0 or start <= prev_end or start < 0:
                    raise InvalidPage(f"page {pid} has invalid interval order")
                if start + length > limit:
                    raise InvalidPage(f"page {pid} interval leaves page range")
                if target not in valid_targets:
                    raise InvalidPage(f"page {pid} targets unknown state {target}")
                frag.append((start, length, target))
                prev_end = start + length - 1
            if effective < 0:
                raise InvalidPage(f"page {pid} has negative row id")
            if effective < index.row_count:
                pass  # concrete fragment
            else:
                alias_index = effective - index.row_count
                if alias_index >= len(index.aliases):
                    raise InvalidPage(f"page {pid} references unknown alias")
                alias = index.aliases[alias_index]
                if alias is None or pid not in alias.override_pages:
                    raise InvalidPage(f"page {pid} carries an undeclared alias entry")
            if effective in result:
                raise InvalidPage(f"page {pid} lists one row twice")
            result[effective] = tuple(frag)
        if r.pos != len(body):
            raise InvalidPage(f"page {pid} has trailing bytes")
    except EOFError as exc:
        raise InvalidPage(f"page {pid} is truncated") from exc
    return result

