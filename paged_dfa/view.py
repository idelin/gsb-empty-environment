"""Read-only, demand-paging query interface over an immutable image."""

from bisect import bisect_left

from .codec import Index, parse_index, parse_page
from .errors import (
    InvalidPage,
    MissingPage,
    PageLoadCancelled,
)
from .model import DEAD, Model

# Page cache states.
_UNKNOWN = "unknown"
_LOADED = "loaded"
_MISSING = "missing"
_CANCELLED = "cancelled"
_INVALID = "invalid"


class InMemoryBackend:
    """Backend holding one image blob; ``omit`` simulates missing pages."""

    def __init__(self, blob: bytes, omit: frozenset[int] = frozenset()):
        self._blob = bytes(blob)
        self._omit = frozenset(omit)

    def fetch(self, pid: int, offset: int, length: int) -> bytes:
        if pid in self._omit:
            raise MissingPage(f"page {pid} is not stored")
        return self._blob[offset:offset + length]


class View:
    """A mapping-like read-only view.

    Pages are fetched and validated lazily.  ``pages_read`` counts distinct
    pages successfully decoded (a page is decoded at most once over the life of
    the view), which lets tests prove that point queries never decompress the
    whole table.
    """

    def __init__(self, source, *, fetcher=None):
        """``source`` is an image blob; ``fetcher(pid, offset, length)`` may
        return bytes, raise MissingPage or PageLoadCancelled."""
        if fetcher is None:
            backend = InMemoryBackend(source)
            fetcher = backend.fetch
        self._index: Index = parse_index(source)  # verify index before opening
        self._fetcher = fetcher
        self._cache: dict[int, dict[int, tuple]] = {}
        self._state: dict[int, str] = {}
        self._pages_read = 0
        self._closed = False

    # --- introspection ----------------------------------------------------
    @property
    def index(self) -> Index:
        return self._index

    @property
    def pages_read(self) -> int:
        return self._pages_read

    @property
    def page_count(self) -> int:
        return self._index.page_count

    @property
    def loaded_pages(self) -> frozenset[int]:
        return frozenset(p for p, s in self._state.items() if s == _LOADED)

    @property
    def logical_model(self) -> Model:
        """Materialize the *current* (alias-aware) logical table.

        Only used by offline checks/tests; a point query never calls this.
        """
        idx = self._index
        rows = []
        for state in range(idx.state_count):
            rid = idx.row_of[state]
            alias = idx.aliases[state] if idx.aliases else None
            if alias is None:
                rows.append(idx.rows[rid])
                continue
            intervals = []
            for pid in range(idx.page_count):
                if pid in alias.override_pages:
                    frag = self._get_page(pid).get(idx.row_count + state, ())
                else:
                    frag = self._get_page(pid).get(rid, ())
                lo = pid * idx.page_size
                intervals.extend((lo + a, b, c) for a, b, c in frag)
            rows.append(_ReconstructedRow(idx.rows[alias.base].default, tuple(intervals)))
        return Model(idx.alphabet, idx.accepting, tuple(rows), tuple(range(idx.state_count)))

    # --- queries ----------------------------------------------------------
    def transition(self, state: int, symbol: int):
        idx = self._index
        if self._closed:
            raise RuntimeError("view has been closed")
        if not isinstance(state, int) or not (0 <= state < idx.state_count):
            raise ValueError(f"unknown state {state!r}")
        pos = bisect_left(idx.alphabet, symbol)
        if pos == len(idx.alphabet) or idx.alphabet[pos] != symbol:
            raise ValueError(f"symbol {symbol!r} is not in the alphabet")

        rid = idx.row_of[state]
        alias = idx.aliases[state] if idx.aliases else None
        if alias is not None:
            pid = pos // idx.page_size
            page = self._get_page(pid)
            lo = pid * idx.page_size
            local = pos - lo
            if pid in alias.override_pages:
                frag = page.get(idx.row_count + state, ())
                default = idx.rows[alias.base].default
            else:
                frag = page.get(rid, ())
                default = idx.rows[rid].default
            target = _lookup_frag(frag, local, default)
        else:
            pid = pos // idx.page_size
            page = self._get_page(pid)
            local = pos - pid * idx.page_size
            target = _lookup_frag(page.get(rid, ()), local, idx.rows[rid].default)
        return target

    def accepts(self, word, start: int = 0) -> bool:
        state = start
        for symbol in word:
            if state == DEAD:
                break
            state = self.transition(state, symbol)
        return state in self._index.accepting

    def _get_page(self, pid: int):
        status = self._state.get(pid, _UNKNOWN)
        if status == _LOADED:
            return self._cache[pid]
        if status == _MISSING:
            raise MissingPage(f"page {pid} is not stored")
        if status == _INVALID:
            raise InvalidPage(f"page {pid} previously failed validation")

        entry = self._index.toc[pid]
        try:
            raw = self._fetcher(pid, entry.offset, entry.length)
        except MissingPage:
            self._state[pid] = _MISSING  # remembered; no page counted as read
            raise
        except PageLoadCancelled:
            self._state[pid] = _CANCELLED  # retryable: nothing cached
            raise
        if raw is None:
            self._state[pid] = _CANCELLED
            raise PageLoadCancelled(f"page {pid} load returned None")
        parsed = parse_page(raw, pid, self._index)
        self._cache[pid] = parsed
        self._state[pid] = _LOADED
        self._pages_read += 1
        return parsed

    def retry(self, pid: int = None):
        """Allow a cancelled load (or cancelled loads) to be attempted again."""
        if pid is None:
            for p in [p for p, s in self._state.items() if s == _CANCELLED]:
                del self._state[p]
        elif self._state.get(pid) == _CANCELLED:
            del self._state[pid]

    def close(self):
        self._closed = True


class _ReconstructedRow:
    __slots__ = ("default", "exceptions")

    def __init__(self, default, exceptions):
        self.default = default
        self.exceptions = exceptions

    def target_at(self, position):
        return _lookup_frag(
            tuple((a, b, c) for a, b, c in self.exceptions),
            position,
            self.default,
        )


def _lookup_frag(fragment, local_pos, default):
    for start, length, target in fragment:
        if start <= local_pos < start + length:
            return target
    return default
