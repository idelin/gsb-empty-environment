"""Append-only durability journal.

Frame layout (integers big-endian)::

    MAGIC (4 bytes "IMJ1") | length (8 bytes) | payload (JSON UTF-8) |
    digest (32 bytes SHA-256 over MAGIC+length+payload)

A record is durable only after ``fsync``.  Recovery scans frame by frame:

* complete, checksum-valid frames are replayed;
* an *incomplete* frame at the very end of the file (fewer bytes than its
  header declares, or a truncated header) is a half-written tail record from
  a crash during append; it is removed by truncation;
* any other structural defect (bad magic with a full header, a complete frame
  whose checksum fails, an undecodable payload, a bad frame with bytes that
  belong to later frames) is corruption in the middle of the log and recovery
  is refused with :class:`JournalCorrupt`.

The asymmetry is deliberate: a torn append can only produce a prefix of the
frame being written, hence it always runs to EOF; a fully present but invalid
frame cannot be explained by a half-write.
"""

import hashlib
import json
import os

MAGIC = b"IMJ1"
HEADER_LEN = 12
DIGEST_LEN = 32

_TORN = object()


def encode(record):
    """Serialize a JSON record into one self-describing journal frame."""
    payload = json.dumps(record, sort_keys=True, separators=(",", ":")).encode("utf-8")
    head = MAGIC + len(payload).to_bytes(8, "big")
    return head + payload + hashlib.sha256(head + payload).digest()


def append(path, record):
    """Durably append one frame with a single write followed by fsync."""
    frame = encode(record)
    with open(path, "ab") as handle:
        handle.write(frame)
        handle.flush()
        os.fsync(handle.fileno())


def read_journal(path):
    """Validate the journal and return ``(records, truncated_bytes)``.

    A torn tail is truncated in place (durable truncate + fsync).  Mid-log
    corruption raises :class:`JournalCorrupt` without modifying the file.
    """
    from .errors import JournalCorrupt

    if not os.path.exists(path):
        return [], 0
    with open(path, "rb") as handle:
        data = handle.read()
    total = len(data)
    records = []
    offset = 0
    while offset < total:
        start = offset
        remaining = total - offset
        if remaining < HEADER_LEN:
            # Truncated header: necessarily the torn tail of the last append.
            return _truncate(path, records, start, total, "short header")
        if data[offset:offset + 4] != MAGIC:
            raise JournalCorrupt(
                f"bad frame magic at byte {offset} with a full header present"
            )
        length = int.from_bytes(data[offset + 4:offset + 12], "big")
        frame_end = offset + HEADER_LEN + length + DIGEST_LEN
        if frame_end > total:
            return _truncate(path, records, start, total, "payload truncated at EOF")
        frame = data[start:frame_end]
        head_payload = frame[:HEADER_LEN + length]
        if hashlib.sha256(head_payload).digest() != frame[HEADER_LEN + length:]:
            raise JournalCorrupt(
                f"checksum mismatch on the complete frame at byte {offset}"
            )
        payload_bytes = data[offset + HEADER_LEN:offset + HEADER_LEN + length]
        try:
            record = json.loads(payload_bytes.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            raise JournalCorrupt(f"undecodable payload at byte {offset}: {exc}") from exc
        records.append(record)
        offset = frame_end
    return records, 0


def _truncate(path, records, start, total, reason):
    """Durably remove the torn tail frame beginning at ``start``."""
    with open(path, "r+b") as handle:
        handle.seek(start)
        handle.truncate(start)
        handle.flush()
        os.fsync(handle.fileno())
    return records, total - start
