"""Compression planner: default targets, row sharing and paged chunks."""

from dataclasses import dataclass, field

from .chunks import encode_chunk

DIR_OWNER = 0
DIR_SHARED_ROW = 1


@dataclass
class PagePlan:
    state: int
    page: int
    payload: bytes
    default: int
    key: bytes          # content hash of the payload
    canon_state: int    # canonical owning state of this row signature


@dataclass
class RowPlan:
    state: int
    canonical: bool
    canon_state: int
    signature: bytes


@dataclass
class EncoderReport:
    page_size: int
    num_states: int
    num_symbols: int
    num_pages: int
    unique_chunks: int
    shared_row_pages: int
    shared_rows: int
    pages_per_state: int
    rows: list = field(default_factory=list)

    def summary(self):
        nominal = self.num_states * self.num_symbols * 2
        return (
            f"page_size={self.page_size} states={self.num_states} "
            f"symbols={self.num_symbols} pages={self.num_pages} "
            f"unique_chunks={self.unique_chunks} "
            f"row_shared_pages={self.shared_row_pages} "
            f"shared_rows={self.shared_rows} "
            f"dense_bytes~{nominal}"
        )


def choose_page_default(cells, sticky, fallback):
    """Pick the most common *implicit* target as the page default.

    Explicit (sticky) cells never participate, so an explicit dead transition
    cannot be swallowed by a default aimed at the dead state.
    """
    counts = {}
    for target, is_sticky in zip(cells, sticky):
        if not is_sticky:
            counts[target] = counts.get(target, 0) + 1
    if not counts:
        return fallback
    # Highest frequency, ties broken by smallest state id for determinism.
    return sorted(counts.items(), key=lambda kv: (-kv[1], kv[0]))[0][0]


def _hash(payload):
    import hashlib
    return hashlib.sha256(payload).digest()


class Encoder:
    """Encodes an explicit DFA into page plans.

    ``page_size`` is given by the caller; the last page of each row may be
    shorter.  Symbol intervals never cross a page boundary (pages are encoded
    independently), which keeps cross-page ranges from ever being misread.
    """

    def __init__(self, dfa, page_size):
        if page_size <= 0:
            raise ValueError("page_size must be positive")
        self.dfa = dfa
        self.page_size = page_size
        self.n = len(dfa.alphabet)
        self.pages_per_state = (self.n + page_size - 1) // page_size

    def plan(self):
        dense = self.dfa.dense_table()
        sparse = self.dfa._sparse  # may be None for dense input

        # Per-state list of (cells, sticky, page_default) per page.
        state_pages = []
        for state in range(self.dfa.num_states):
            row = dense[state]
            sticky = self._row_sticky(state, row, sparse)
            pages = []
            for page in range(self.pages_per_state):
                lo = page * self.page_size
                hi = min(lo + self.page_size, self.n)
                cells = row[lo:hi]
                marks = sticky[lo:hi]
                fallback = row[0]
                default = choose_page_default(cells, marks, fallback)
                payload = encode_chunk(cells, default, marks)
                pages.append((cells, marks, default, payload))
            state_pages.append(pages)

        # Whole-row sharing: identical sequence of page contents shares blobs.
        signatures = [
            _hash(b"".join(payload for _, _, _, payload in pages))
            for pages in state_pages
        ]
        canon_of = {}
        row_plans = []
        for state, sig in enumerate(signatures):
            if sig not in canon_of:
                canon_of[sig] = state
                row_plans.append(RowPlan(state, True, state, sig))
            else:
                row_plans.append(
                    RowPlan(state, False, canon_of[sig], sig)
                )

        page_plans = []
        unique = {}
        for state, pages in enumerate(state_pages):
            for page, (cells, marks, default, payload) in enumerate(pages):
                key = _hash(payload)
                unique.setdefault(key, payload)
                page_plans.append(
                    PagePlan(state, page, payload, default, key,
                             canon_of[signatures[state]])
                )

        shared_rows = sum(1 for r in row_plans if not r.canonical)
        report = EncoderReport(
            page_size=self.page_size,
            num_states=self.dfa.num_states,
            num_symbols=self.n,
            num_pages=len(page_plans),
            unique_chunks=len(unique),
            shared_row_pages=shared_rows * self.pages_per_state,
            shared_rows=shared_rows,
            pages_per_state=self.pages_per_state,
            rows=row_plans,
        )
        return page_plans, report, unique

    def _row_sticky(self, state, row, sparse):
        n = self.n
        if sparse is None or state not in sparse:
            # Dense input: every cell is an explicit declaration.
            return [True] * n
        _default, runs = sparse[state]
        sticky = [False] * n
        for lo, hi, _target in runs:
            for pos in range(lo, hi):
                sticky[pos] = True
        return sticky
