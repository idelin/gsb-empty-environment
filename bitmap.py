"""Composable, compressed row-selection bitmap.

A :class:`Bitmap` selects rows from a fixed-length row space.  The index
space is split into fixed-size segments; every segment is stored in the
cheapest of two compressed forms:

* ``sparse`` -- a sorted list of set-bit offsets, for segments with few
  bits set, and
* ``dense``  -- an array of 64-bit words, for segments with many bits set.

Bitmaps compose with ``&`` (AND), ``|`` (OR) and ``~`` (NOT) and answer
``rank``/``select`` random-access queries.  NOT only flips bits inside the
valid row range ``[0, len)``; padding bits in the final segment never leak
into any result.  Combining bitmaps of different lengths raises
:class:`BitmapLengthError`.
"""

from __future__ import annotations

from bisect import bisect_left, bisect_right

CHUNK_BITS = 4096
_WORD_BITS = 64
_WORDS_PER_CHUNK = CHUNK_BITS // _WORD_BITS
_WORD_MASK = (1 << _WORD_BITS) - 1
# Segments with at most this many set bits stay in sparse form.
SPARSE_THRESHOLD = 256


class BitmapLengthError(ValueError):
    """Raised when combining bitmaps that cover different row counts."""


class _Segment:
    """One compressed segment covering CHUNK_BITS consecutive positions."""

    __slots__ = ("sparse", "words", "card")

    def __init__(self, sparse, words, card):
        self.sparse = sparse  # sorted list[int] or None
        self.words = words    # list of 64-bit ints or None
        self.card = card

    # -- constructors ---------------------------------------------------
    @classmethod
    def from_offsets(cls, offsets):
        offs = sorted(set(offsets))
        card = len(offs)
        if card <= SPARSE_THRESHOLD:
            return cls(offs, None, card)
        words = [0] * _WORDS_PER_CHUNK
        for off in offs:
            words[off >> 6] |= 1 << (off & 63)
        return cls(None, words, card)

    @classmethod
    def from_words(cls, words):
        card = 0
        for w in words:
            card += w.bit_count()
        if card <= SPARSE_THRESHOLD:
            return cls(cls._words_to_offsets(words), None, card)
        return cls(None, list(words), card)

    @classmethod
    def full(cls, valid_bits):
        """All-ones segment with exactly ``valid_bits`` low bits set."""
        nwords = -(-valid_bits // _WORD_BITS)
        words = [_WORD_MASK] * nwords + [0] * (_WORDS_PER_CHUNK - nwords)
        extra = valid_bits & 63
        if extra:
            words[nwords - 1] = (1 << extra) - 1
        return cls.from_words(words)

    @staticmethod
    def _words_to_offsets(words):
        offs = []
        for wi, w in enumerate(words):
            base = wi << 6
            while w:
                offs.append(base + (w & -w).bit_length() - 1)
                w &= w - 1
        return offs

    def to_words(self):
        if self.words is not None:
            return list(self.words)
        words = [0] * _WORDS_PER_CHUNK
        for off in self.sparse:
            words[off >> 6] |= 1 << (off & 63)
        return words

    # -- boolean ops ------------------------------------------------------
    def and_(self, other):
        if self.sparse is not None and other.sparse is not None:
            keep = set(other.sparse)
            return _Segment.from_offsets([o for o in self.sparse if o in keep])
        if self.sparse is not None:
            return _Segment.from_offsets(
                [o for o in self.sparse
                 if other.words[o >> 6] >> (o & 63) & 1])
        if other.sparse is not None:
            return _Segment.from_offsets(
                [o for o in other.sparse
                 if self.words[o >> 6] >> (o & 63) & 1])
        return _Segment.from_words(
            [a & b for a, b in zip(self.words, other.words)])

    def or_(self, other):
        if self.sparse is not None and other.sparse is not None:
            return _Segment.from_offsets(self.sparse + other.sparse)
        return _Segment.from_words(
            [a | b for a, b in zip(self.to_words(), other.to_words())])

    def not_(self, valid_bits):
        """Flip bits below ``valid_bits``; higher bits stay zero."""
        words = [w ^ _WORD_MASK for w in self.to_words()]
        nwords = -(-valid_bits // _WORD_BITS)
        for wi in range(nwords, _WORDS_PER_CHUNK):
            words[wi] = 0
        extra = valid_bits & 63
        if extra:
            words[nwords - 1] &= (1 << extra) - 1
        return _Segment.from_words(words)

    # -- rank / select ----------------------------------------------------
    def rank_within(self, off):
        """Count set bits at offsets 0..off inclusive."""
        if self.sparse is not None:
            return bisect_right(self.sparse, off)
        wi, bit = divmod(off, _WORD_BITS)
        total = 0
        for w in self.words[:wi]:
            total += w.bit_count()
        return total + (self.words[wi] & ((1 << (bit + 1)) - 1)).bit_count()

    def select_within(self, j):
        """Offset of the j-th set bit (1-based) inside this segment."""
        if self.sparse is not None:
            return self.sparse[j - 1]
        for wi, w in enumerate(self.words):
            pc = w.bit_count()
            if j > pc:
                j -= pc
                continue
            while j > 1:
                w &= w - 1
                j -= 1
            return (wi << 6) + (w & -w).bit_length() - 1
        raise IndexError(j)  # unreachable when callers respect cardinality


class Bitmap:
    """Compressed selection bitmap over a fixed number of rows."""

    __slots__ = ("_length", "_segments", "_count")

    def __init__(self, length, segments=()):
        if length < 0:
            raise ValueError("bitmap length must be >= 0")
        self._length = length
        segs = []
        count = 0
        for seg in segments:
            if seg is None or seg.card == 0:
                segs.append(None)
            else:
                segs.append(seg)
                count += seg.card
        while segs and segs[-1] is None:
            segs.pop()
        self._segments = segs
        self._count = count

    # -- constructors -----------------------------------------------------
    @classmethod
    def zeros(cls, length):
        return cls(length)

    @classmethod
    def ones(cls, length):
        nseg = -(-length // CHUNK_BITS)
        return cls(length, [
            _Segment.full(min(CHUNK_BITS, length - i * CHUNK_BITS))
            for i in range(nseg)
        ])

    @classmethod
    def from_indices(cls, length, indices):
        idx = sorted(set(indices))
        for i in idx:
            if not 0 <= i < length:
                raise IndexError(f"index {i} out of range for length {length}")
        nseg = -(-length // CHUNK_BITS)
        buckets = [[] for _ in range(nseg)]
        for i in idx:
            buckets[i // CHUNK_BITS].append(i % CHUNK_BITS)
        return cls(length, [_Segment.from_offsets(b) for b in buckets])

    @classmethod
    def from_bools(cls, flags):
        flags = list(flags)
        return cls.from_indices(len(flags),
                                [i for i, f in enumerate(flags) if f])

    # -- basic accessors ----------------------------------------------------
    def __len__(self):
        return self._length

    def count(self):
        """Number of selected rows (popcount)."""
        return self._count

    def __bool__(self):
        return self._count > 0

    def __repr__(self):
        return f"Bitmap(len={self._length}, count={self._count})"

    def __eq__(self, other):
        if not isinstance(other, Bitmap):
            return NotImplemented
        return (self._length == other._length
                and self._count == other._count
                and list(self) == list(other))

    def _segment(self, i):
        return self._segments[i] if i < len(self._segments) else None

    def __contains__(self, i):
        if not 0 <= i < self._length:
            return False
        seg = self._segment(i // CHUNK_BITS)
        if seg is None:
            return False
        off = i % CHUNK_BITS
        if seg.sparse is not None:
            pos = bisect_left(seg.sparse, off)
            return pos < len(seg.sparse) and seg.sparse[pos] == off
        return bool(seg.words[off >> 6] >> (off & 63) & 1)

    def __iter__(self):
        """Yield selected row indices in ascending order."""
        for si, seg in enumerate(self._segments):
            if seg is None:
                continue
            base = si * CHUNK_BITS
            offs = seg.sparse if seg.sparse is not None \
                else _Segment._words_to_offsets(seg.words)
            for off in offs:
                yield base + off

    def to_indices(self):
        return list(self)

    def segment_kinds(self):
        """Storage kind of every segment: 'sparse', 'dense' or 'empty'."""
        nseg = -(-self._length // CHUNK_BITS)
        kinds = []
        for i in range(nseg):
            seg = self._segment(i)
            if seg is None:
                kinds.append("empty")
            elif seg.sparse is not None:
                kinds.append("sparse")
            else:
                kinds.append("dense")
        return kinds

    # -- boolean algebra ----------------------------------------------------
    def _check_compatible(self, other):
        if not isinstance(other, Bitmap):
            raise TypeError(f"cannot combine Bitmap with {type(other).__name__}")
        if self._length != other._length:
            raise BitmapLengthError(
                f"cannot combine bitmaps of length {self._length} "
                f"and {other._length}")

    def __and__(self, other):
        self._check_compatible(other)
        nseg = max(len(self._segments), len(other._segments))
        out = []
        for i in range(nseg):
            a, b = self._segment(i), other._segment(i)
            out.append(None if a is None or b is None else a.and_(b))
        return Bitmap(self._length, out)

    def __or__(self, other):
        self._check_compatible(other)
        nseg = max(len(self._segments), len(other._segments))
        out = []
        for i in range(nseg):
            a, b = self._segment(i), other._segment(i)
            if a is None:
                out.append(b)
            elif b is None:
                out.append(a)
            else:
                out.append(a.or_(b))
        return Bitmap(self._length, out)

    def __invert__(self):
        nseg = -(-self._length // CHUNK_BITS)
        out = []
        for i in range(nseg):
            valid = min(CHUNK_BITS, self._length - i * CHUNK_BITS)
            seg = self._segment(i)
            out.append(seg.not_(valid) if seg is not None
                       else _Segment.full(valid))
        return Bitmap(self._length, out)

    # -- rank / select --------------------------------------------------------
    def rank(self, i):
        """Number of selected rows in positions 0..i (inclusive)."""
        if i < 0:
            return 0
        if i >= self._length:
            return self._count
        si, off = divmod(i, CHUNK_BITS)
        total = 0
        for s in range(min(si, len(self._segments))):
            seg = self._segments[s]
            if seg is not None:
                total += seg.card
        seg = self._segment(si)
        if seg is not None:
            total += seg.rank_within(off)
        return total

    def select(self, k):
        """Position of the k-th selected row (1-based)."""
        if not 1 <= k <= self._count:
            raise IndexError(
                f"select({k}) out of range: bitmap selects {self._count} rows")
        remaining = k
        for si, seg in enumerate(self._segments):
            if seg is None:
                continue
            if remaining > seg.card:
                remaining -= seg.card
            else:
                return si * CHUNK_BITS + seg.select_within(remaining)
        raise IndexError(k)  # unreachable: k <= count guarantees a hit
