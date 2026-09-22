"""Binary wire format primitives.

All integers use unsigned LEB128 (uvarint); signed destinations (state ids are
non-negative, :data:`~paged_dfa.model.DEAD` is -1) use zig-zag svarint.

The container is laid out as::

    +------------------------- index -------------------------+
    | magic | ver | tag | page_size | alphabet | accepting     |
    |       | state_count | alias_count | aliases | rows        |
    |       | page_count | toc[] | index_digest(16)             |
    +----------------------------------------------------------+
    | page 0 blob | page 1 blob | ...                          |
    +----------------------------------------------------------+

``toc`` is ``(row_id, offset, length, payload_digest16)`` per page.
``index_digest`` is keyed BLAKE2b over every index byte that precedes it.

Each page blob is::

    uvarint page_id | uvarint entry_count | entries... | checksum16

An entry is ``svarint effective_row_id | uvarint interval_count |
interval[]`` with interval = ``uvarint start_pos | uvarint length |
svarint target``.  ``checksum16`` is keyed BLAKE2b over
``page_id || payload_bytes || index_salt`` where the salt is the BLAKE2b of the
index region, so moving a page between images is always detected.
"""

MAGIC = b"PDFA"
FORMAT_VERSION = 1
_DIGEST_SIZE = 16


def uvarint(value: int) -> bytes:
    if value < 0:
        raise ValueError("uvarint requires a non-negative value")
    out = bytearray()
    while True:
        chunk = value & 0x7F
        value >>= 7
        if value:
            out.append(chunk | 0x80)
        else:
            out.append(chunk)
            return bytes(out)


def _zigzag(value: int) -> int:
    return (value << 1) ^ (value >> 63) if value >= 0 else (~value << 1) | 1


def svarint(value: int) -> bytes:
    return uvarint((value << 1) ^ (value >> 63))


class Reader:
    """Cursor over a memoryview of bytes; every read is bounds checked."""

    __slots__ = ("buf", "pos")

    def __init__(self, buf, pos: int = 0):
        self.buf = buf
        self.pos = pos

    def take(self, count: int) -> memoryview:
        end = self.pos + count
        if end > len(self.buf):
            raise EOFError("unexpected end of container")
        chunk = self.buf[self.pos:end]
        self.pos = end
        return chunk

    def bytes(self, count: int) -> bytes:
        return bytes(self.take(count))

    def uv(self) -> int:
        shift = 0
        result = 0
        while True:
            byte = self.take(1)[0]
            result |= (byte & 0x7F) << shift
            if not byte & 0x80:
                return result
            shift += 7
            if shift >= 70:
                raise ValueError("uvarint too long")

    def sv(self) -> int:
        raw = self.uv()
        return (raw >> 1) ^ -(raw & 1)


class Writer:
    __slots__ = ("buf",)

    def __init__(self):
        self.buf = bytearray()

    def put(self, data) -> "Writer":
        self.buf.extend(data)
        return self

    def u(self, value: int) -> "Writer":
        self.buf.extend(uvarint(value))
        return self

    def s(self, value: int) -> "Writer":
        self.buf.extend(svarint(value))
        return self

    def finish(self) -> bytes:
        return bytes(self.buf)


def digest(data, key: bytes = b"") -> bytes:
    import hashlib

    return hashlib.blake2b(bytes(data), digest_size=_DIGEST_SIZE, key=key).digest()
