"""Single transition-page chunk: default target + interval exceptions.

Wire format (little endian)::

    k          H   number of cells in this page (<= page_size)
    default    H   default target state id
    flags      H   bit0: sticky bitmap present
    runs       H   number of exception intervals
    run        (I start, I end, H target) * runs   half-open, symbol ids
    sticky     ceil(k/8) bytes, bit j of byte i marks cell (8*i+j) explicit

A *sticky* (explicit) cell always wins over the default, even when its target
equals the default.  This is what stops a default target (including a default
that points at the dead state) from hiding an explicit dead transition.
"""

import struct

_HEAD = struct.Struct("<HHHH")
_RUN = struct.Struct("<IIH")
FLAG_STICKY = 1


def _bitmap_bytes(k):
    return (k + 7) // 8


def _segments(cells, default, sticky):
    """Yield (lo, hi, target) exception segments.

    A cell is exceptional when it is explicit (sticky) or its target differs
    from the default; adjacent exceptional cells with equal targets coalesce.
    """
    k = len(cells)
    i = 0
    while i < k:
        if not sticky[i] and cells[i] == default:
            i += 1
            continue
        target = cells[i]
        j = i + 1
        while j < k:
            if not sticky[j] and cells[j] == default:
                break
            if cells[j] != target:
                break
            j += 1
        yield i, j, target
        i = j


def encode_chunk(cells, default, sticky):
    """Build a chunk payload.

    ``cells`` is the list of targets for this page; ``sticky`` a bool list of
    the same length marking explicit cells.
    """
    k = len(cells)
    if len(sticky) != k:
        raise ValueError("sticky length must match cells length")
    if not (0 <= default < 0x10000):
        raise ValueError("target ids must fit in uint16")
    runs = list(_segments(cells, default, sticky))
    flags = FLAG_STICKY if any(sticky) else 0
    out = bytearray()
    out += _HEAD.pack(k, default, flags, len(runs))
    if flags & FLAG_STICKY:
        raw = bytearray(_bitmap_bytes(k))
        for pos, marked in enumerate(sticky):
            if marked:
                raw[pos >> 3] |= 1 << (pos & 7)
        out += raw
    for lo, hi, target in runs:
        out += _RUN.pack(lo, hi, target)
    return bytes(out)


class DecodedChunk:
    __slots__ = ("k", "default", "runs", "sticky")

    def __init__(self, k, default, runs, sticky):
        self.k = k
        self.default = default
        self.runs = runs
        self.sticky = sticky  # tuple[bool]

    def cell(self, pos):
        target = self.default
        for lo, hi, value in self.runs:
            if lo <= pos < hi:
                target = value
                break
        return target

    def cells(self):
        row = [self.default] * self.k
        for lo, hi, value in self.runs:
            for p in range(lo, hi):
                row[p] = value
        return row

    def is_sticky(self, pos):
        return self.sticky[pos]


def decode_chunk(payload, expected_k=None):
    """Decode and self-validate a chunk payload."""
    from .errors import PatchError

    if len(payload) < _HEAD.size:
        raise PatchError("chunk truncated")
    k, default, flags, nruns = _HEAD.unpack_from(payload, 0)
    off = _HEAD.size
    sticky = [False] * k
    if flags & FLAG_STICKY:
        need = _bitmap_bytes(k)
        if off + need > len(payload):
            raise PatchError("sticky bitmap truncated")
        bitmap = payload[off:off + need]
        off += need
        for pos in range(k):
            if bitmap[pos >> 3] & (1 << (pos & 7)):
                sticky[pos] = True
    runs = []
    prev_end = -1
    for _ in range(nruns):
        if off + _RUN.size > len(payload):
            raise PatchError("chunk runs truncated")
        lo, hi, target = _RUN.unpack_from(payload, off)
        off += _RUN.size
        if not (0 <= lo < hi <= k) or lo < prev_end:
            raise PatchError(f"invalid or overlapping run [{lo},{hi})")
        runs.append((lo, hi, target))
        prev_end = hi
    if off != len(payload):
        raise PatchError("trailing bytes in chunk")
    if expected_k is not None and k != expected_k:
        raise PatchError(f"chunk length {k} != expected {expected_k}")
    return DecodedChunk(k, default, runs, tuple(sticky))
