"""Reversible per-page deltas with copy-on-write row isolation.

A :class:`PageDelta` records, for one ``(state, page)``:

* the page blob offset it was generated against (tamper detection),
* a sequence of :class:`CellEdit` / :class:`DefaultEdit` operations,
* an accepting-set / start-state change for the new version (optional).

Applying a delta decodes only the affected page, mutates a *fresh* chunk
(copy-on-write, so a shared row is detached) and appends a new blob.  The
inverse delta flips every edit, restoring the previous bytes exactly.
"""

import json
import struct
from dataclasses import dataclass

from .chunks import decode_chunk, encode_chunk
from .container import decode_blob_at
from .encoder import choose_page_default
from .errors import PatchError

DELTA_MAGIC = b"PDLT"


@dataclass(frozen=True)
class CellEdit:
    """Set cell at local position ``pos`` to ``target``.

    ``explicit`` records whether the cell is a declared transition.  Default
    edits never erase explicit (including explicit dead) transitions.
    """

    pos: int
    target: int
    explicit: bool = True


@dataclass(frozen=True)
class DefaultEdit:
    """Change the page default target (explicit cells are retained)."""

    new_default: int


@dataclass
class PageDelta:
    state: int
    page: int
    base_offset: int
    edits: list
    accepting: frozenset = None
    start: int = None

    def to_payload(self):
        ops = []
        for edit in self.edits:
            if isinstance(edit, CellEdit):
                ops.append({"t": "c", "p": edit.pos, "v": edit.target,
                            "x": int(edit.explicit)})
            elif isinstance(edit, DefaultEdit):
                ops.append({"t": "d", "v": edit.new_default})
            else:
                raise PatchError("unknown edit type")
        body = {
            "state": self.state,
            "page": self.page,
            "base": self.base_offset,
            "ops": ops,
            "accepting": (sorted(self.accepting)
                          if self.accepting is not None else None),
            "start": self.start,
        }
        return DELTA_MAGIC + json.dumps(body, separators=(",", ":")).encode()


def parse_delta_payload(payload):
    if len(payload) < 4 or payload[:4] != DELTA_MAGIC:
        raise PatchError("delta blob has bad magic")
    data = json.loads(payload[4:])
    edits = []
    for op in data["ops"]:
        if op["t"] == "c":
            edits.append(CellEdit(op["p"], op["v"], bool(op["x"])))
        elif op["t"] == "d":
            edits.append(DefaultEdit(op["v"]))
        else:
            raise PatchError("unknown delta op")
    accepting = (frozenset(data["accepting"])
                 if data.get("accepting") is not None else None)
    return PageDelta(
        data["state"], data["page"], data["base"], edits,
        accepting=accepting, start=data.get("start"),
    )


def _apply_edits(chunk, edits, page_size, num_symbols):
    default = chunk.default
    for edit in edits:
        if isinstance(edit, DefaultEdit):
            default = edit.new_default
    cells = []
    for pos in range(chunk.k):
        cells.append(chunk.default)
    for lo, hi, target in chunk.runs:
        for pos in range(lo, hi):
            cells[pos] = target
    sticky = list(chunk.sticky)
    for edit in edits:
        if isinstance(edit, CellEdit):
            if not (0 <= edit.pos < chunk.k):
                raise PatchError(
                    f"cell edit position {edit.pos} crosses page size"
                )
            cells[edit.pos] = edit.target
            sticky[edit.pos] = edit.explicit
        elif not isinstance(edit, DefaultEdit):
            raise PatchError("unknown edit")
    return cells, sticky, default


def inverse_delta(delta, base_payload, page_size, page):
    """Construct the delta that undoes ``delta``."""
    chunk = decode_chunk(base_payload)
    inverse = [DefaultEdit(chunk.default)]
    for edit in delta.edits:
        if isinstance(edit, CellEdit):
            inverse.append(
                CellEdit(edit.pos, chunk.cell(edit.pos),
                         chunk.is_sticky(edit.pos))
            )
    return PageDelta(delta.state, delta.page, -1, inverse)


def apply_delta(writer, base_view, delta, *, note=""):
    """Apply one-page delta on ``writer`` against ``base_view``.

    Returns ``(new_version_id, new_version_offset, inverse_payload_bytes)``.
    The caller keeps ``base_view`` open; it remains fully queryable.
    """
    state, page = delta.state, delta.page
    entry = base_view.root.entries.get((state, page))
    if entry is None:
        raise PatchError(f"base page ({state},{page}) missing")
    offset, flags, owner = entry
    if offset != delta.base_offset:
        raise PatchError(
            f"delta base {delta.base_offset} != current blob {offset}"
        )

    raw = writer._read_blob(offset)
    page_k = base_view._page_cells_count(page)
    chunk = decode_chunk(raw, expected_k=page_k)
    cells, sticky, default = _apply_edits(
        chunk, delta.edits, writer.page_size, base_view.num_symbols
    )

    inverse = inverse_delta(delta, raw, writer.page_size, page)
    new_payload = encode_chunk(cells, default, sticky)

    # Copy-on-write: always append a new blob; shared rows stay untouched.
    new_offset = writer._append_blob(new_payload)
    delta_offset = writer._append_blob(delta.to_payload())
    entries = dict(base_view.root.entries)
    entries[(state, page)] = (new_offset, 0, state)
    accepting = (delta.accepting if delta.accepting is not None
                 else base_view.accepting)
    start = delta.start if delta.start is not None else base_view.start
    new_id, new_root = writer.commit_version(
        entries,
        accepting=accepting,
        start=start,
        note=note,
        parent_version=base_view.version_id,
        delta_blob=delta_offset,
    )
    return new_id, new_root, inverse.to_payload(), delta_offset


def make_inverse_version(writer, version_view, delta_payload, *, note="inverse"):
    """Apply the inverse of a stored delta, rolling ``version_view`` back.

    The inverse is constructed against the parent version (the version the
    delta was generated from), so page bytes, default target, accepting set
    and start state are all restored.
    """
    delta = parse_delta_payload(delta_payload)
    parent = writer.view(version_offset=version_view.root.parent_offset) if (
        version_view.root.parent_offset is not None
    ) else None
    try:
        if parent is None:
            raise PatchError("delta has no parent version to restore")
        parent_entry = parent.root.entries[(delta.state, delta.page)]
        raw = writer._read_blob(parent_entry[0])
        back = inverse_delta(delta, raw, writer.page_size, delta.page)
        back.base_offset = entry_current_offset(version_view, delta)
        back.accepting = parent.accepting
        back.start = parent.start
        return apply_delta(writer, version_view, back, note=note)
    finally:
        if parent is not None:
            parent.close()


def entry_current_offset(version_view, delta):
    return version_view.root.entries[(delta.state, delta.page)][0]
