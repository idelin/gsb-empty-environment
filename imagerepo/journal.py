"""Append-only, checksummed journal.

Frame layout on disk::

    MAGIC (16 bytes) once at file creation
    repeated records:
        uint32 BE crc32(JSON payload)
        uint8  type           (currently only 1 = JSON)
        uint32 BE length
        bytes  payload       (exactly ``length`` bytes)

A crash can only leave a torn *tail*: a complete prefix of valid frames
followed by zero or more partial/garbage bytes. Recovery truncates that tail.
Corruption followed by another valid frame is *middle corruption* and makes
recovery refuse to proceed (a torn tail plus a valid frame cannot be produced
by crashes + atomic appends, because a crash never allows a later write).
"""
from __future__ import annotations

import json
import os
import struct
import zlib
from dataclasses import dataclass

MAGIC = b"IMAGEREPO-JNL001"
HEADER_LEN = len(MAGIC)
FRAME_HDR = struct.Struct(">IBI")
MAX_RECORD = 64 * 1024 * 1024


class JournalCorrupt(Exception):
    """Unrecoverable middle corruption (must not be truncated away)."""


class JournalBadMagic(JournalCorrupt):
    pass


@dataclass
class Record:
    seq: int
    rec_type: int
    payload: dict


def _is_zero_tail(chunk: bytes) -> bool:
    return chunk == b"\x00" * len(chunk)


def _scan_payload(buf: bytes, offset: int, length: int):
    """Check one payload frame starting at ``offset`` (frame header included).

    Returns (record_or_None, next_offset_or_None, ok_bool).
    """
    end = offset + FRAME_HDR.size + length
    if end > len(buf):
        return None, None, False
    crc, rec_type, declared = FRAME_HDR.unpack_from(buf, offset)
    if declared != length:
        return None, None, False
    payload = buf[offset + FRAME_HDR.size : end]
    if zlib.crc32(payload) & 0xFFFFFFFF != crc:
        return None, None, False
    return payload, end, True


def _valid_chain_at(buf: bytes, offset: int) -> bool:
    """True iff a valid frame chain starts at ``offset`` and ends exactly EOF."""
    pos = offset
    while pos < len(buf):
        if pos + FRAME_HDR.size > len(buf):
            return False
        crc, rec_type, length = FRAME_HDR.unpack_from(buf, pos)
        if length > MAX_RECORD:
            return False
        payload, nxt, ok = _scan_payload(buf, pos, length)
        if not ok:
            return False
        pos = nxt
    return True


def _chain_starts_later(buf: bytes, after: int) -> bool:
    """Search the remainder for a frame-aligned valid chain reaching EOF."""
    pos = after + 1
    while pos < len(buf):
        if _valid_chain_at(buf, pos):
            return True
        pos += 1
    return False


def scan_journal(path: str) -> tuple[list[Record], int, bool]:
    """Read and validate the journal.

    Returns ``(records, valid_end, truncated)``.  ``truncated`` is True when a
    torn tail was removed on disk.  Raises :class:`JournalCorrupt` for a bad
    magic or middle corruption.
    """
    with open(path, "rb") as fh:
        buf = fh.read()
    if len(buf) < HEADER_LEN:
        if buf and not _is_zero_tail(buf):
            raise JournalCorrupt("non-empty partial header in journal")
        return [], HEADER_LEN, False
    if buf[:HEADER_LEN] != MAGIC:
        raise JournalBadMagic("journal magic mismatch")

    records: list[Record] = []
    pos = HEADER_LEN
    seq = 0
    torn = False
    while pos < len(buf):
        if pos + FRAME_HDR.size > len(buf):
            tail = buf[pos:]
            if not _is_zero_tail(tail):
                torn = True
            break
        crc, rec_type, length = FRAME_HDR.unpack_from(buf, pos)
        if length > MAX_RECORD:
            if _chain_starts_later(buf, pos):
                raise JournalCorrupt(f"oversized frame at {pos} followed by valid data")
            torn = True
            break
        end = pos + FRAME_HDR.size + length
        if end > len(buf):
            # Whole tail is this one partially written frame -> torn tail.
            torn = True
            break
        payload = buf[pos + FRAME_HDR.size : end]
        if zlib.crc32(payload) & 0xFFFFFFFF != crc:
            # A bad frame with a complete valid chain later in the file is
            # middle corruption (crashes can never create a later record).
            if _chain_starts_later(buf, pos):
                raise JournalCorrupt(
                    f"bad checksum/partial frame at {pos} followed by valid frames")
            torn = True
            break
        try:
            data = json.loads(payload.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError):
            if _chain_starts_later(buf, pos):
                raise JournalCorrupt(f"unparseable record at {pos} followed by valid frames")
            torn = True
            break
        records.append(Record(seq=seq, rec_type=rec_type, payload=data))
        seq += 1
        pos = end

    if torn and pos != len(buf):
        _truncate(path, pos)
        return records, pos, True
    return records, pos, False


def _truncate(path: str, end: int) -> None:
    with open(path, "r+b") as fh:
        fh.truncate(end)
        fh.flush()
        os.fsync(fh.fileno())
    fsync_dir(os.path.dirname(path))


def fsync_dir(path: str) -> None:
    """fsync a directory; no-op on platforms without the feature."""
    try:
        fd = os.open(path, os.O_RDONLY)
    except OSError:
        return
    try:
        os.fsync(fd)
    except OSError:
        pass
    finally:
        os.close(fd)


def atomic_write(path: str, data: bytes, *, tmp_dir: str | None = None) -> None:
    """Write ``data`` to a sibling temp file, fsync, then atomic rename."""
    directory = os.path.dirname(path) or "."
    tmp_dir = tmp_dir or directory
    tmp = os.path.join(tmp_dir, f".tmp-{os.path.basename(path)}-{os.getpid()}-{id(data)}")
    with open(tmp, "wb") as fh:
        fh.write(data)
        fh.flush()
        os.fsync(fh.fileno())
    os.replace(tmp, path)
    fsync_dir(directory)


class Journal:
    """Writer/reader over the append-only journal."""

    def __init__(self, directory: str):
        self.directory = directory
        self.path = os.path.join(directory, "journal.log")

    @classmethod
    def open(cls, directory: str, *, create: bool = True) -> "Journal":
        os.makedirs(directory, exist_ok=True)
        journal = cls(directory)
        if not os.path.exists(journal.path):
            if not create:
                raise FileNotFoundError(journal.path)
            with open(journal.path, "wb") as fh:
                fh.write(MAGIC)
                fh.flush()
                os.fsync(fh.fileno())
            fsync_dir(directory)
        return journal

    def append(self, payload: dict, *, rec_type: int = 1) -> Record:
        body = json.dumps(payload, sort_keys=True, separators=(",", ":")).encode("utf-8")
        if len(body) > MAX_RECORD:
            raise ValueError("journal record too large")
        frame = FRAME_HDR.pack(zlib.crc32(body) & 0xFFFFFFFF, rec_type, len(body)) + body
        # Single append; POSIX guarantees the update of one file offset via
        # O_APPEND is atomic relative to other appends.
        with open(self.path, "ab") as fh:
            fh.write(frame)
            fh.flush()
            os.fsync(fh.fileno())
        records, _, _ = scan_journal(self.path)
        return Record(seq=len(records) - 1, rec_type=rec_type, payload=payload)

    def records(self) -> list[Record]:
        return scan_journal(self.path)[0]
