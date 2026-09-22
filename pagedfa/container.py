"""Append-only single-file container.

On-disk layout
==============

Header (64 bytes)::

    magic        4s  b"PDFA"
    version      H   format version (1)
    flags        H   reserved
    page_size    I   symbols per page
    num_states   I
    num_symbols  I
    active_root  Q   file offset of the active version-root blob
    reserved     32s

Blob::

    b"BL" | length I | payload (length bytes) | crc32 I | b"BE"

Reads are served through an ``mmap`` mapping (read-only).  Writers append new
blobs at end of file and publish a new version root by overwriting the
8-byte ``active_root`` field; an existing mmap keeps the old root alive, so
old views are never disturbed.
"""

import mmap
import os
import struct
import zlib

MAGIC = b"PDFA"
FORMAT_VERSION = 1
HEADER_SIZE = 64
BLOB_PREFIX = b"BL"
BLOB_SUFFIX = b"BE"
_BLOB_HEAD = struct.Struct("<2sI")   # marker, payload length
_BLOB_TAIL = struct.Struct("<I2s")   # crc, end marker


class HeaderError(Exception):
    """The file header is not a valid paged-DFA container."""


def write_header(fd, page_size, num_states, num_symbols):
    header = _pack_header(page_size, num_states, num_symbols, active_root=0)
    os.lseek(fd, 0, os.SEEK_SET)
    os.write(fd, header)


def _pack_header(page_size, num_states, num_symbols, active_root):
    fixed = struct.pack(
        "<4sHHIIIQ",
        MAGIC,
        FORMAT_VERSION,
        0,
        page_size,
        num_states,
        num_symbols,
        active_root,
    )
    return fixed + b"\x00" * (HEADER_SIZE - len(fixed))


def read_header(buf):
    """Return ``(page_size, num_states, num_symbols, active_root)``."""
    if len(buf) < HEADER_SIZE or buf[:4] != MAGIC:
        raise HeaderError("bad magic; not a paged-DFA container")
    magic, version, _flags, page_size, nstates, nsyms, active_root = struct.unpack(
        "<4sHHIIIQ", buf[:28]
    )
    if version != FORMAT_VERSION:
        raise HeaderError(f"unsupported format version {version}")
    if page_size == 0:
        raise HeaderError("page_size must be non-zero")
    return page_size, nstates, nsyms, active_root


def encode_blob(payload):
    crc = zlib.crc32(payload) & 0xFFFFFFFF
    return (
        _BLOB_HEAD.pack(BLOB_PREFIX, len(payload))
        + payload
        + _BLOB_TAIL.pack(crc, BLOB_SUFFIX)
    )


def decode_blob_at(buf, offset, *, file_size=None):
    """Validate framing and CRC of a blob; return its payload.

    Raises ``CorruptOffsetError`` for any out-of-bounds / framing problem and
    ``BadChecksumError`` for a framing-valid blob with a wrong CRC.
    """
    from .errors import BadChecksumError, CorruptOffsetError

    if file_size is None:
        file_size = len(buf)
    if offset < 0 or offset + _BLOB_HEAD.size > file_size:
        raise CorruptOffsetError(f"blob offset {offset} out of bounds")
    marker, length = _BLOB_HEAD.unpack_from(buf, offset)
    if marker != BLOB_PREFIX:
        raise CorruptOffsetError(f"blob offset {offset} has no begin marker")
    end = offset + _BLOB_HEAD.size + length + _BLOB_TAIL.size
    if end > file_size:
        raise CorruptOffsetError(f"blob at {offset} extends past end of file")
    payload_start = offset + _BLOB_HEAD.size
    payload = bytes(buf[payload_start:payload_start + length])
    crc, tail_marker = _BLOB_TAIL.unpack_from(buf, payload_start + length)
    if tail_marker != BLOB_SUFFIX:
        raise CorruptOffsetError(f"blob at {offset} has no end marker")
    if zlib.crc32(payload) & 0xFFFFFFFF != crc:
        raise BadChecksumError(f"blob at {offset} failed CRC32")
    return payload


def publish_active_root(fd, root_offset):
    """Atomically publish a version root (8-byte aligned field overwrite)."""
    os.lseek(fd, 20, os.SEEK_SET)
    os.write(fd, struct.pack("<Q", root_offset))
    os.fsync(fd)


def open_ro_mmap(path):
    """Open a file read-only and return ``(file_object, mmap, file_size)``."""
    fd = os.open(path, os.O_RDONLY)
    try:
        size = os.fstat(fd).st_size
        if size < HEADER_SIZE:
            os.close(fd)
            raise HeaderError("file too small to contain a header")
        mapping = mmap.mmap(fd, size, access=mmap.ACCESS_READ)
    except BaseException:
        os.close(fd)
        raise
    wrapper = _OwnedFile(fd)
    return wrapper, mapping, size


class _OwnedFile:
    def __init__(self, fd):
        self.fd = fd

    def close(self):
        try:
            os.close(self.fd)
        except OSError:
            pass
