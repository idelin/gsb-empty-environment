"""Persistent write-ahead log and checkpoints.

Record framing (binary)::

    [4-byte big-endian payload length][JSON payload (UTF-8)][4-byte CRC32]

The file begins with a magic line ``WMLGv1\\n``.

Recovery rules (see :func:`read_log`)
* A record that fails length/CRC validation is called a *bad frame*.
* A bad frame that reaches EOF (only NUL/zero tail bytes, or no valid frame
  afterwards) is a *torn tail*: it is discarded, recovery continues.
* A bad frame followed by any valid frame (an internal resync marker exists)
  means *middle corruption*: opening the log raises :class:`CorruptLogError`.

Checkpoints are written atomically (temp file + ``os.replace``) and carry
their own CRC32.  A corrupt checkpoint is ignored: replay then resumes from
the journal position stored in the checkpoint, or from the very beginning.
"""
from __future__ import annotations

import json
import os
import struct
import zlib

from .model import command_from_dict, command_to_dict
from .model import Command

MAGIC = b"WMLGv1\n"
_HEADER = struct.Struct(">I")

# Record kinds (durable journal entries).
R_COMMAND = "CMD"
R_DECISION = "DECISION"
R_PUBLISH = "PUBLISH"
R_CONFIRM = "CONFIRM"
R_HANDOFF_DONE = "HANDOFF_DONE"


class CorruptLogError(Exception):
    """Raised when a log contains unrecoverable middle corruption."""


# ---------------------------------------------------------------------------
# Framing
# ---------------------------------------------------------------------------


def frame(payload: bytes) -> bytes:
    crc = zlib.crc32(payload) & 0xFFFFFFFF
    return _HEADER.pack(len(payload)) + payload + _HEADER.pack(crc)


def _frame_ok(frame_bytes: bytes) -> bool:
    if len(frame_bytes) < 8:
        return False
    (length,) = _HEADER.unpack_from(frame_bytes, 0)
    total = 4 + length + 4
    if len(frame_bytes) != total:
        return False
    payload = frame_bytes[4 : 4 + length]
    (crc,) = _HEADER.unpack_from(frame_bytes, 4 + length)
    return zlib.crc32(payload) & 0xFFFFFFFF == crc


def scan_frames(blob: bytes) -> tuple[list[tuple[int, bytes]], int, bool, bool]:
    """Scan raw bytes into frames.

    Returns ``(frames, good_end, torn_tail, middle_corrupt)`` where each frame
    is ``(body_relative_start_offset, frame_bytes)``.  A bad frame with any
    valid frame after it is middle corruption (fatal); a bad frame confined to
    EOF is a torn tail (truncatable).
    """
    offset = 0
    frames: list[tuple[int, bytes]] = []
    size = len(blob)
    saw_bad_before_good = False
    last_good_end = 0
    bad_offsets: list[int] = []
    while offset < size:
        if size - offset < 4:
            bad_offsets.append(offset)
            break
        (length,) = _HEADER.unpack_from(blob, offset)
        total = 4 + length + 4
        if length == 0 or size - offset < total:
            bad_offsets.append(offset)
            break
        candidate = blob[offset : offset + total]
        if _frame_ok(candidate):
            frames.append((offset, candidate))
            offset += total
            last_good_end = offset
        else:
            # Try to resync at the next plausible frame start.
            bad_offsets.append(offset)
            nxt = offset + 1
            resync = -1
            while nxt < size - 8:
                (cand_len,) = _HEADER.unpack_from(blob, nxt)
                cand_total = 4 + cand_len + 4
                if cand_len and nxt + cand_total <= size and _frame_ok(
                    blob[nxt : nxt + cand_total]
                ):
                    resync = nxt
                    break
                nxt += 1
            if resync == -1:
                break
            saw_bad_before_good = True
            offset = resync
    torn_tail = bool(bad_offsets) and not saw_bad_before_good
    middle_corrupt = saw_bad_before_good
    return frames, last_good_end, torn_tail, middle_corrupt


# ---------------------------------------------------------------------------
# Record encoding
# ---------------------------------------------------------------------------


def encode_record(kind: str, txn: int, payload: dict) -> bytes:
    return frame(
        json.dumps(
            {"k": kind, "t": txn, "p": payload},
            ensure_ascii=False,
            sort_keys=True,
        ).encode("utf-8")
    )


def decode_frame(frame_bytes: bytes) -> dict:
    (length,) = _HEADER.unpack_from(frame_bytes, 0)
    raw = frame_bytes[4 : 4 + length]
    return json.loads(raw.decode("utf-8"))


def encode_command(txn: int, command: Command) -> bytes:
    return encode_record(R_COMMAND, txn, command_to_dict(command))


# ---------------------------------------------------------------------------
# Log storage
# ---------------------------------------------------------------------------


class BaseLog:
    """Common record-level log over a byte-addressable append-only store."""

    def __init__(self) -> None:
        self._records: list[tuple[int, dict]] = []
        self._next_txn = 1
        self._size = len(MAGIC)

    # -- storage hooks -----------------------------------------------------

    def _write(self, data: bytes) -> int:  # pragma: no cover - overridden
        raise NotImplementedError

    def read_all(self) -> bytes:  # pragma: no cover - overridden
        raise NotImplementedError

    def truncate(self, size: int) -> None:  # pragma: no cover - overridden
        raise NotImplementedError

    # -- appends -----------------------------------------------------------

    @property
    def size(self) -> int:
        return self._size

    def append_raw(self, encoded: bytes) -> int:
        offset = self._size
        self._write(encoded)
        self._size += len(encoded)
        return offset

    def append_command(self, command: Command) -> tuple[int, int]:
        txn = self._next_txn
        self._next_txn += 1
        offset = self.append_raw(encode_command(txn, command))
        self._records.append((txn, {"k": R_COMMAND, "p": command}))
        return txn, offset

    def append_effect(self, kind: str, txn: int, payload: dict) -> int:
        offset = self.append_raw(encode_record(kind, txn, payload))
        self._records.append((txn, {"k": kind, "p": payload}))
        return offset

    # -- reads -------------------------------------------------------------

    def read_records(self) -> list[tuple[int, dict]]:
        """Decode every durable record; repair torn tail; reject middle rot."""
        blob = self.read_all()
        if blob.startswith(MAGIC):
            body = blob[len(MAGIC) :]
        elif len(blob) < len(MAGIC):
            # Partial magic at EOF: treat as torn tail.
            if MAGIC.startswith(blob):
                body = b""
            else:
                raise CorruptLogError("invalid log magic")
        else:
            raise CorruptLogError("invalid log magic")
        frames, good_end, torn_tail, middle = scan_frames(body)
        if middle:
            raise CorruptLogError("middle corruption detected in journal")
        final_size = len(MAGIC) + good_end
        if torn_tail:
            # Discard the torn tail physically so later appends stay aligned.
            self.truncate(final_size)
            self._size = final_size
        records: list[tuple[int, dict, int, int]] = []
        for body_offset, frame_bytes in frames:
            entry = decode_frame(frame_bytes)
            payload = entry["p"]
            if entry["k"] == R_COMMAND:
                payload = command_from_dict(payload)
            start = len(MAGIC) + body_offset
            end = start + len(frame_bytes)
            records.append(
                (entry["t"], {"k": entry["k"], "p": payload}, start, end)
            )
        if records:
            # Continue transaction numbering where the durable log left off,
            # even if this log object is a fresh handle on an existing file.
            self._next_txn = max(txn for txn, _, _, _ in records) + 1
        return records


class MemoryLog(BaseLog):
    """In-memory log used by tests and the discrete-event simulator."""

    def __init__(self) -> None:
        super().__init__()
        self._buf = bytearray(MAGIC)

    def _write(self, data: bytes) -> None:
        self._buf.extend(data)

    def read_all(self) -> bytes:
        return bytes(self._buf)

    def truncate(self, size: int) -> None:
        del self._buf[size:]
        self._size = size


class FileLog(BaseLog):
    """File-backed log.

    Appends are flushed with ``os.fsync`` (process-crash semantics; power-loss
    guarantees of the underlying file system are out of scope).
    """

    def __init__(self, path: str) -> None:
        super().__init__()
        self.path = path
        new = not os.path.exists(path) or os.path.getsize(path) == 0
        self._fh = open(path, "a+b", buffering=0)
        if new:
            self._fh.write(MAGIC)
            self._fh.flush()
            os.fsync(self._fh.fileno())
            self._size = len(MAGIC)
        else:
            self._size = os.path.getsize(path)

    def _write(self, data: bytes) -> None:
        self._fh.write(data)
        self._fh.flush()
        os.fsync(self._fh.fileno())

    def read_all(self) -> bytes:
        with open(self.path, "rb") as handle:
            return handle.read()

    def truncate(self, size: int) -> None:
        self._fh.seek(size)
        self._fh.truncate(size)
        self._fh.flush()
        os.fsync(self._fh.fileno())
        self._size = size
        self._fh.seek(0, os.SEEK_END)

    def close(self) -> None:
        self._fh.close()


# ---------------------------------------------------------------------------
# Checkpoints
# ---------------------------------------------------------------------------


def write_checkpoint(path: str, snapshot: dict) -> None:
    """Atomically write a checksummed checkpoint."""
    body = json.dumps(snapshot, ensure_ascii=False, sort_keys=True).encode(
        "utf-8"
    )
    crc = zlib.crc32(body) & 0xFFFFFFFF
    tmp = f"{path}.tmp"
    with open(tmp, "wb") as handle:
        handle.write(_HEADER.pack(crc))
        handle.write(body)
        handle.flush()
        os.fsync(handle.fileno())
    os.replace(tmp, path)


def read_checkpoint(path: str) -> dict | None:
    """Read and verify a checkpoint; return None if missing or corrupt."""
    if not os.path.exists(path):
        return None
    with open(path, "rb") as handle:
        raw = handle.read()
    if len(raw) < 4:
        return None
    (crc,) = _HEADER.unpack_from(raw, 0)
    body = raw[4:]
    if zlib.crc32(body) & 0xFFFFFFFF != crc:
        return None
    try:
        return json.loads(body.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError):
        return None
