"""Append-only versioned store for paged transition tables."""

import json
import os
import struct

from .chunks import decode_chunk, encode_chunk
from .container import (
    HEADER_SIZE,
    HeaderError,
    decode_blob_at,
    encode_blob,
    open_ro_mmap,
    publish_active_root,
    read_header,
    write_header,
)
from .encoder import DIR_OWNER, DIR_SHARED_ROW, Encoder
from .errors import (
    BadChecksumError,
    CorruptOffsetError,
    IndexError as PIndexError,
    LoadCancelledError,
    MissingPageError,
)

_DIR_ENTRY = struct.Struct("<IIQBH")  # state, page, blob_offset, flags, owner_state
_ROOT_MAGIC = b"VROOT"


class _Provider:
    """Resolves a (state, page) request to raw chunk payload bytes."""


class MMapProvider(_Provider):
    """Default provider: reads chunks through a read-only mmap."""

    def __init__(self, view):
        self._view = view

    def get_page(self, state, page):
        view = self._view
        offset = view.dir_offset(state, page)
        if offset is None:
            raise MissingPageError(state, page)
        return decode_blob_at(view.mapping, offset, file_size=view.file_size)


class CallbackProvider(_Provider):
    """Provider driven by a user callback for custom fetch / cancellation."""

    def __init__(self, callback):
        self._callback = callback

    def get_page(self, state, page):
        result = self._callback(state, page)
        if result is None:
            raise MissingPageError(state, page)
        if isinstance(result, Exception):
            if isinstance(result, LoadCancelledError):
                raise result
            raise LoadCancelledError(state, page, str(result))
        return bytes(result)


class _VersionRoot:
    """Parsed version-root payload (directory + metadata)."""

    def __init__(self, data, root_offset):
        self.root_offset = root_offset
        self.version_id = data["version_id"]
        self.parent_version = data.get("parent_version")
        self.parent_offset = data.get("parent_offset")
        self.delta_blob = data.get("delta_blob")  # offset or None
        self.accepting = frozenset(data["accepting"])
        self.start = data["start"]
        self.note = data.get("note", "")
        entries = data["entries"]
        self.entries = {}
        for e in entries:
            self.entries[(e["state"], e["page"])] = (
                e["offset"], e["flags"], e["owner"]
            )

    def to_blob(self):
        entries = [
            {"state": s, "page": p, "offset": off, "flags": fl, "owner": owner}
            for (s, p), (off, fl, owner) in sorted(self.entries.items())
        ]
        payload_dict = {
            "magic": _ROOT_MAGIC.decode(),
            "version_id": self.version_id,
            "parent_version": self.parent_version,
            "parent_offset": getattr(self, "parent_offset", None),
            "delta_blob": self.delta_blob,
            "start": self.start,
            "accepting": sorted(self.accepting),
            "note": self.note,
            "entries": entries,
        }
        body = json.dumps(payload_dict, separators=(",", ":")).encode()
        return _ROOT_MAGIC + body


def _parse_root_payload(payload, root_offset):
    if len(payload) < 5 or payload[:5] != _ROOT_MAGIC:
        raise CorruptOffsetError("version root has bad magic")
    try:
        data = json.loads(payload[5:])
    except ValueError as exc:
        raise CorruptOffsetError(f"version root is not valid JSON: {exc}")
    for key in ("version_id", "start", "accepting", "entries"):
        if key not in data:
            raise CorruptOffsetError(f"version root missing field {key!r}")
    return _VersionRoot(data, root_offset)


class Stats:
    """Counts page reads actually served (never full-table decompression)."""

    def __init__(self):
        self.page_loads = 0
        self.page_hits = 0
        self.accessed_pages = []

    def snapshot(self):
        return {
            "page_loads": self.page_loads,
            "page_hits": self.page_hits,
            "distinct_pages": len({(s, p) for s, p in self.accessed_pages}),
        }

    def reset(self):
        self.page_loads = 0
        self.page_hits = 0
        self.accessed_pages = []


class TableView:
    """Read-only query view over one committed version.

    Opening a view validates the header, version root and directory (index
    ranges and every target state) *before* any query is possible.  Page
    payloads themselves are verified lazily on first access (and eagerly with
    :meth:`verify_pages`), so corrupt/missing/cancelled pages only surface when
    that page is genuinely read.
    """

    def __init__(self, path, *, provider=None, version_offset=None, validate=True):
        self.path = path
        self._owned_file, self.mapping, self.file_size = open_ro_mmap(path)
        self.page_size, self.num_states, self.num_symbols, active_root = \
            read_header(self.mapping)
        root_offset = version_offset if version_offset is not None else active_root
        if root_offset == 0:
            raise CorruptOffsetError("no version root has been published yet")
        root_payload = decode_blob_at(
            self.mapping, root_offset, file_size=self.file_size
        )
        self.root = _parse_root_payload(root_payload, root_offset)
        self._page_cache = {}
        self.stats = Stats()
        self.provider = provider if provider is not None else MMapProvider(self)
        self.pages_per_state = (
            self.num_symbols + self.page_size - 1
        ) // self.page_size
        self._closed = False
        if validate:
            self._validate_directory()

    # ---- lifecycle ----------------------------------------------------------
    def close(self):
        if self._closed:
            return
        self._closed = True
        try:
            self.mapping.close()
        finally:
            self._owned_file.close()

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()

    # ---- metadata -----------------------------------------------------------
    @property
    def version_id(self):
        return self.root.version_id

    @property
    def start(self):
        return self.root.start

    @property
    def accepting(self):
        return self.root.accepting

    def dir_offset(self, state, page):
        entry = self.root.entries.get((state, page))
        if entry is None:
            return None
        return entry[0]

    def has_page(self, state, page):
        return (state, page) in self.root.entries

    def canonical_owner(self, state, page):
        return self.root.entries[(state, page)][2]

    # ---- validation ---------------------------------------------------------
    def _validate_directory(self):
        nstates = self.num_states
        npages = self.pages_per_state
        if not (0 <= self.root.start < nstates):
            raise PIndexError("start state outside declared state range")
        for q in self.root.accepting:
            if not (0 <= q < nstates):
                raise BadTargetError("accepting state outside declared range")
        for (state, page), (offset, flags, owner) in self.root.entries.items():
            if not (0 <= state < nstates) or not (0 <= page < npages):
                raise PIndexError(
                    f"directory index ({state},{page}) outside declared grid"
                )
            if not (0 <= owner < nstates):
                raise BadTargetError(f"shared-row owner {owner} out of range")
            if flags not in (DIR_OWNER, DIR_SHARED_ROW):
                raise CorruptOffsetError(f"unknown directory flags {flags}")
            if HEADER_SIZE <= offset < self.file_size:
                decode_blob_at(self.mapping, offset, file_size=self.file_size)
            elif offset == -1 or offset is None or not (
                HEADER_SIZE <= offset < self.file_size
            ):
                raise CorruptOffsetError(
                    f"directory offset for ({state},{page}) points outside file"
                )

    def verify_pages(self):
        """Force validation of every declared page; returns page count."""
        count = 0
        for state in range(self.num_states):
            for page in range(self.pages_per_state):
                self._load_chunk(state, page)
                count += 1
        return count

    # ---- queries ------------------------------------------------------------
    def _locate(self, state, symbol_id):
        if not (0 <= state < self.num_states):
            raise PIndexError(f"state {state} out of range")
        if not (0 <= symbol_id < self.num_symbols):
            raise PIndexError(f"symbol id {symbol_id} out of range")
        page, pos = divmod(symbol_id, self.page_size)
        return page, pos

    def _page_cells_count(self, page):
        lo = page * self.page_size
        return min(self.page_size, self.num_symbols - lo)

    def _load_chunk(self, state, page):
        cached = self._page_cache.get((state, page))
        if cached is not None:
            self.stats.page_hits += 1
            return cached
        entry = self.root.entries.get((state, page))
        if entry is None:
            raise MissingPageError(state, page)
        raw = self.provider.get_page(state, page)
        chunk = decode_chunk(raw, expected_k=self._page_cells_count(page))
        if not (0 <= chunk.default < self.num_states):
            raise BadTargetError(
                f"page ({state},{page}) default {chunk.default} out of range"
            )
        for _lo, _hi, target in chunk.runs:
            if not (0 <= target < self.num_states):
                raise BadTargetError(
                    f"page ({state},{page}) target {target} out of range"
                )
        self._page_cache[(state, page)] = chunk
        self.stats.page_loads += 1
        self.stats.accessed_pages.append((state, page))
        return chunk

    def transition(self, state, symbol_id):
        """Return the target for one cell; validates before answering."""
        page, pos = self._locate(state, symbol_id)
        chunk = self._load_chunk(state, page)
        return chunk.cell(pos)

    def is_explicit(self, state, symbol_id):
        page, pos = self._locate(state, symbol_id)
        return self._load_chunk(state, page).is_sticky(pos)

    def dense_table(self):
        """Full materialization (used for cell-by-cell reference checks)."""
        table = []
        for state in range(self.num_states):
            row = []
            for page in range(self.pages_per_state):
                row.extend(self._load_chunk(state, page).cells())
            table.append(row)
        return table

    def accepts(self, state):
        return state in self.root.accepting


class TableWriter:
    """Creates a container and publishes hot-swappable versions."""

    def __init__(self, path, dfa, page_size, *, note="v1"):
        self.path = path
        self.page_size = page_size
        self.dfa = dfa
        self.fd = os.open(path, os.O_RDWR | os.O_CREAT | os.O_TRUNC, 0o644)
        write_header(self.fd, page_size, dfa.num_states, len(dfa.alphabet))
        self._lock_exclusive()
        os.fsync(self.fd)
        self._next_version = 1

    @classmethod
    def create(cls, path, dfa, page_size, *, note="v1"):
        writer = cls(path, dfa, page_size, note=note)
        plan, report, _unique = Encoder(dfa, page_size).plan()
        entries = {}
        for pp in plan:
            if pp.state == pp.canon_state:
                offset = writer._append_blob(pp.payload)
                entries[(pp.state, pp.page)] = (offset, DIR_OWNER, pp.canon_state)
            else:
                owner_offset = entries[(pp.canon_state, pp.page)][0]
                entries[(pp.state, pp.page)] = (
                    owner_offset, DIR_SHARED_ROW, pp.canon_state
                )
        root = _VersionRoot.__new__(_VersionRoot)
        root.root_offset = None
        root.version_id = writer._next_version
        root.parent_version = None
        root.parent_offset = None
        root.delta_blob = None
        root.accepting = dfa.accepting
        root.start = dfa.start
        root.note = note
        root.entries = entries
        writer._publish(root)
        writer._last_report = report
        return writer

    def _lock_exclusive(self):
        try:
            import fcntl
            fcntl.flock(self.fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError:
            pass  # single-process test environment; best effort

    def _read_blob(self, offset):
        os.lseek(self.fd, offset, os.SEEK_SET)
        head = os.read(self.fd, 6)
        if len(head) < 6 or head[:2] != b"BL":
            raise CorruptOffsetError("writer cannot read blob header")
        import struct
        length = struct.unpack("<I", head[2:6])[0]
        payload = b""
        while len(payload) < length:
            chunk = os.read(self.fd, length - len(payload))
            if not chunk:
                raise CorruptOffsetError("blob truncated")
            payload += chunk
        return payload

    def _append_blob(self, payload):
        os.lseek(self.fd, 0, os.SEEK_END)
        offset = os.lseek(self.fd, 0, os.SEEK_CUR)
        os.write(self.fd, encode_blob(payload))
        return offset

    def _publish(self, root):
        if root.parent_offset is None and getattr(self, "active_version_offset", None):
            root.parent_offset = self.active_version_offset
        offset = self._append_blob(root.to_blob())
        os.fsync(self.fd)
        publish_active_root(self.fd, offset)
        self.active_version_id = root.version_id
        self.active_version_offset = offset
        self._next_version = root.version_id + 1

    def view(self, *, provider=None, version_offset=None):
        return TableView(
            self.path, provider=provider, version_offset=version_offset
        )

    def close(self):
        if self.fd is not None:
            os.close(self.fd)
            self.fd = None

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()

    # ---- hot swap -----------------------------------------------------------
    def commit_version(self, entries, *, accepting, start, note="",
                       parent_version=None, delta_blob=None):
        """Publish a new generation; previous views keep their old root."""
        root = _VersionRoot.__new__(_VersionRoot)
        root.root_offset = None
        root.version_id = self._next_version
        root.parent_version = parent_version
        root.parent_offset = getattr(self, "_last_root_offset", None)
        root.delta_blob = delta_blob
        root.accepting = frozenset(accepting)
        root.start = start
        root.note = note
        root.entries = dict(entries)
        self._publish(root)
        return self.active_version_id, self.active_version_offset
