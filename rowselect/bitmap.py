"""Composable compressed row-selection bitmap with rank/select.

Storage is segmented into fixed-size chunks. Each chunk is stored as:

- ``0``        -- empty segment (sparse all-zero region, O(1) memory)
- ``_FULL``    -- full segment (dense all-one region, O(1) memory)
- ``int``      -- dense bitmask for partially populated segments

All logical operations (AND/OR/XOR/NOT) are chunk-wise and re-normalize
segments afterwards, so sparse and dense regions stay compressed. NOT only
flips bits inside the valid row range: tail padding bits in the last chunk
are masked off and can never leak into results.
"""

from __future__ import annotations

_CHUNK_BITS = 512
_FULL = -1  # sentinel: segment entirely set (only valid for full-size chunks)


def _mask(bits: int) -> int:
    return (1 << bits) - 1


class Bitmap:
    """An immutable-by-convention selection bitmap over ``length`` rows."""

    __slots__ = ("length", "_chunks")

    def __init__(self, length: int, chunks: list[int] | None = None):
        if length < 0:
            raise ValueError(f"length must be >= 0, got {length}")
        self.length = length
        n_chunks = (length + _CHUNK_BITS - 1) // _CHUNK_BITS
        if chunks is None:
            self._chunks = [0] * n_chunks
        else:
            if len(chunks) != n_chunks:
                raise ValueError(
                    f"expected {n_chunks} chunks, got {len(chunks)}"
                )
            self._chunks = [self._norm(c, i) for i, c in enumerate(chunks)]

    # ------------------------------------------------------------------
    # constructors
    # ------------------------------------------------------------------
    @classmethod
    def zeros(cls, length: int) -> "Bitmap":
        return cls(length)

    @classmethod
    def ones(cls, length: int) -> "Bitmap":
        n_chunks = (length + _CHUNK_BITS - 1) // _CHUNK_BITS
        return cls(length, [_FULL] * n_chunks)

    @classmethod
    def from_indices(cls, length: int, indices) -> "Bitmap":
        bm = cls(length)
        for i in indices:
            bm._set(i)
        return bm

    @classmethod
    def from_bits(cls, bits) -> "Bitmap":
        bits = list(bits)
        bm = cls(len(bits))
        for i, b in enumerate(bits):
            if b:
                bm._set(i)
        return bm

    # ------------------------------------------------------------------
    # segment helpers
    # ------------------------------------------------------------------
    def _chunk_bits(self, idx: int) -> int:
        """Number of valid (non-padding) rows covered by chunk ``idx``."""
        return min(_CHUNK_BITS, self.length - idx * _CHUNK_BITS)

    def _norm(self, chunk: int, idx: int) -> int:
        """Normalize a raw chunk value and mask off tail padding bits."""
        bits = self._chunk_bits(idx)
        m = _mask(bits)
        if chunk == _FULL or chunk == m:
            # All valid bits set: compress to the sentinel when possible.
            return _FULL if bits == _CHUNK_BITS else m
        return chunk & m

    def _resolve(self, idx: int) -> int:
        """Materialize chunk ``idx`` as a plain bitmask int."""
        c = self._chunks[idx]
        if c == _FULL:
            return _mask(self._chunk_bits(idx))
        return c

    def _set(self, i: int) -> None:
        if not 0 <= i < self.length:
            raise IndexError(f"index {i} out of range for length {self.length}")
        idx, off = divmod(i, _CHUNK_BITS)
        self._chunks[idx] = self._norm(self._resolve(idx) | (1 << off), idx)

    # ------------------------------------------------------------------
    # logical composition
    # ------------------------------------------------------------------
    def _binary(self, other: "Bitmap", op) -> "Bitmap":
        if not isinstance(other, Bitmap):
            raise TypeError(f"cannot combine Bitmap with {type(other).__name__}")
        if self.length != other.length:
            raise ValueError(
                f"bitmap length mismatch: {self.length} != {other.length}"
            )
        out = [
            op(self._resolve(i), other._resolve(i))
            for i in range(len(self._chunks))
        ]
        return Bitmap(self.length, out)

    def __and__(self, other: "Bitmap") -> "Bitmap":
        return self._binary(other, int.__and__)

    def __or__(self, other: "Bitmap") -> "Bitmap":
        return self._binary(other, int.__or__)

    def __xor__(self, other: "Bitmap") -> "Bitmap":
        return self._binary(other, int.__xor__)

    def __invert__(self) -> "Bitmap":
        # Flip only valid rows; tail padding bits are masked off by _norm.
        out = [
            (~self._resolve(i)) & _mask(self._chunk_bits(i))
            for i in range(len(self._chunks))
        ]
        return Bitmap(self.length, out)

    # ------------------------------------------------------------------
    # rank / select
    # ------------------------------------------------------------------
    def rank(self, i: int) -> int:
        """Number of selected rows in the half-open range ``[0, i)``."""
        if not 0 <= i <= self.length:
            raise IndexError(f"rank({i}) out of range for length {self.length}")
        idx, off = divmod(i, _CHUNK_BITS)
        total = 0
        for j in range(idx):
            c = self._chunks[j]
            total += _CHUNK_BITS if c == _FULL else c.bit_count()
        if off:
            total += (self._resolve(idx) & _mask(off)).bit_count()
        return total

    def select(self, k: int) -> int:
        """Row index of the k-th (0-based) selected row."""
        if k < 0:
            raise IndexError(f"select({k}) out of range")
        remaining = k
        for i in range(len(self._chunks)):
            c = self._chunks[i]
            count = _CHUNK_BITS if c == _FULL else c.bit_count()
            if remaining < count:
                bits = self._resolve(i)
                while bits:
                    lsb = bits & -bits
                    if remaining == 0:
                        return i * _CHUNK_BITS + lsb.bit_length() - 1
                    remaining -= 1
                    bits ^= lsb
            else:
                remaining -= count
        raise IndexError(
            f"select({k}) out of range: only {self.popcount()} rows selected"
        )

    def popcount(self) -> int:
        """Total number of selected rows."""
        return self.rank(self.length)

    # ------------------------------------------------------------------
    # accessors
    # ------------------------------------------------------------------
    def __len__(self) -> int:
        return self.length

    def __contains__(self, i: int) -> bool:
        if not 0 <= i < self.length:
            return False
        idx, off = divmod(i, _CHUNK_BITS)
        return bool((self._resolve(idx) >> off) & 1)

    def __iter__(self):
        for i in range(len(self._chunks)):
            bits = self._resolve(i)
            base = i * _CHUNK_BITS
            while bits:
                lsb = bits & -bits
                yield base + lsb.bit_length() - 1
                bits ^= lsb

    def __bool__(self) -> bool:
        return any(self._chunks)

    def to_indices(self) -> list[int]:
        return list(self)

    def __eq__(self, other) -> bool:
        return (
            isinstance(other, Bitmap)
            and self.length == other.length
            and all(
                self._resolve(i) == other._resolve(i)
                for i in range(len(self._chunks))
            )
        )

    def __repr__(self) -> str:
        return f"Bitmap(length={self.length}, selected={self.popcount()})"
