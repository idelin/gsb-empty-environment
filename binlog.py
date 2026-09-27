#!/usr/bin/env python3
"""Append-only binary log with per-frame segmentation and validation.

Frame layout (all integers big-endian):

    +------------------+-----------------------------------------------+
    | length  (4B)     | uint32, payload length in bytes               |
    | seq     (8B)     | uint64, sequence number, starts at 0, +1 each |
    | prev    (32B)    | SHA-256 digest of the previous frame's full   |
    |                  | encoded bytes; 32 zero bytes for frame 0      |
    | crc32   (4B)     | uint32 CRC-32 of (seq || prev || payload)     |
    | payload (length) | opaque bytes                                  |
    +------------------+-----------------------------------------------+

A reader consumes the log as a stream of arbitrary byte chunks.  At end of
stream the tail is classified as either CLEAN (ends exactly on a frame
boundary) or TRUNCATED (a final frame was only partially written).  Any
violation inside a *complete* frame (bad CRC, bad sequence, broken
prev-digest chain, implausible length) is CORRUPTION and is never treated
as an ignorable tail: recovery only ever truncates an incomplete tail and
refuses to skip corrupt frames.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import struct
import sys
import zlib

LEN_FMT = ">I"
SEQ_FMT = ">Q"
CRC_FMT = ">I"
LEN_SIZE = struct.calcsize(LEN_FMT)      # 4
SEQ_SIZE = struct.calcsize(SEQ_FMT)      # 8
CRC_SIZE = struct.calcsize(CRC_FMT)      # 4
DIGEST_SIZE = hashlib.sha256().digest_size  # 32
HEADER_SIZE = LEN_SIZE + SEQ_SIZE + DIGEST_SIZE + CRC_SIZE  # 48
MAX_PAYLOAD = 64 * 1024 * 1024  # sanity bound for the length prefix
GENESIS_DIGEST = b"\x00" * DIGEST_SIZE

CLEAN = "clean"
TRUNCATED = "truncated"


class CorruptionError(Exception):
    """Raised when a complete frame (or its length prefix) is invalid.

    Carries the absolute byte ``offset`` at which the bad frame starts and
    a human-readable ``reason``.  Corruption is never recoverable by
    truncation; the log must be repaired externally.
    """

    def __init__(self, offset: int, reason: str):
        super().__init__(f"corruption at offset {offset}: {reason}")
        self.offset = offset
        self.reason = reason


def _crc(seq: int, prev_digest: bytes, payload: bytes) -> int:
    return zlib.crc32(struct.pack(SEQ_FMT, seq) + prev_digest + payload) & 0xFFFFFFFF


def encode_frame(seq: int, payload: bytes, prev_digest: bytes) -> bytes:
    """Encode one frame."""
    if len(payload) > MAX_PAYLOAD:
        raise ValueError("payload too large")
    crc = _crc(seq, prev_digest, payload)
    return (
        struct.pack(LEN_FMT, len(payload))
        + struct.pack(SEQ_FMT, seq)
        + prev_digest
        + struct.pack(CRC_FMT, crc)
        + payload
    )


def frame_digest(frame_bytes: bytes) -> bytes:
    return hashlib.sha256(frame_bytes).digest()


class StreamReader:
    """Incremental validator.  Feed arbitrary byte chunks; frames are
    emitted as soon as they are complete and verified."""

    def __init__(self) -> None:
        self._buf = bytearray()
        self.next_seq = 0
        self.prev_digest = GENESIS_DIGEST
        self.valid_bytes = 0  # offset just past the last verified frame
        self.frames: list[dict] = []  # {seq, offset, length, total, payload}

    def feed(self, data: bytes) -> list[dict]:
        self._buf += data
        return self._drain()

    def _drain(self) -> list[dict]:
        out = []
        while True:
            if len(self._buf) < LEN_SIZE:
                break
            (plen,) = struct.unpack(LEN_FMT, self._buf[:LEN_SIZE])
            if plen > MAX_PAYLOAD:
                raise CorruptionError(self.valid_bytes, f"implausible length {plen}")
            total = HEADER_SIZE + plen
            if len(self._buf) < total:
                break  # incomplete tail so far; decided at finish()
            frame = bytes(self._buf[:total])
            seq = struct.unpack(SEQ_FMT, frame[LEN_SIZE : LEN_SIZE + SEQ_SIZE])[0]
            prev = frame[LEN_SIZE + SEQ_SIZE : LEN_SIZE + SEQ_SIZE + DIGEST_SIZE]
            crc = struct.unpack(CRC_FMT, frame[LEN_SIZE + SEQ_SIZE + DIGEST_SIZE : HEADER_SIZE])[0]
            payload = frame[HEADER_SIZE:]
            if crc != _crc(seq, prev, payload):
                raise CorruptionError(self.valid_bytes, "crc32 mismatch")
            if seq != self.next_seq:
                raise CorruptionError(
                    self.valid_bytes, f"sequence gap: expected {self.next_seq}, got {seq}"
                )
            if prev != self.prev_digest:
                raise CorruptionError(self.valid_bytes, "prev-digest chain broken")
            rec = {
                "seq": seq,
                "offset": self.valid_bytes,
                "length": plen,
                "total": total,
                "payload": payload,
            }
            self.frames.append(rec)
            out.append(rec)
            self.prev_digest = frame_digest(frame)
            self.next_seq += 1
            self.valid_bytes += total
            del self._buf[:total]
        return out

    def finish(self) -> tuple[str, int]:
        """Classify the end of stream.

        Returns (CLEAN, 0) if the stream ends on a frame boundary, or
        (TRUNCATED, n) where n is the number of leftover bytes belonging to
        an incompletely written final frame.  Only this leftover tail may
        be truncated away.
        """
        if not self._buf:
            return CLEAN, 0
        return TRUNCATED, len(self._buf)


def verify_bytes(data: bytes, chunk_size: int = 65536) -> dict:
    """Verify a byte string, feeding it in chunk_size pieces."""
    reader = StreamReader()
    for i in range(0, len(data), chunk_size):
        reader.feed(data[i : i + chunk_size])
    status, tail = reader.finish()
    return {
        "status": status,
        "frames": len(reader.frames),
        "valid_bytes": reader.valid_bytes,
        "tail_bytes": tail,
        "records": reader.frames,
    }


def verify_file(path: str, chunk_size: int = 65536) -> dict:
    reader = StreamReader()
    with open(path, "rb") as fh:
        while True:
            chunk = fh.read(chunk_size)
            if not chunk:
                break
            reader.feed(chunk)
    status, tail = reader.finish()
    return {
        "status": status,
        "frames": len(reader.frames),
        "valid_bytes": reader.valid_bytes,
        "tail_bytes": tail,
        "records": reader.frames,
    }


def recover_file(path: str) -> dict:
    """Truncate an incomplete tail in place.

    Raises CorruptionError if any complete frame is invalid: corrupt frames
    are never skipped or discarded by recovery.
    """
    info = verify_file(path)
    if info["status"] == TRUNCATED:
        with open(path, "r+b") as fh:
            fh.truncate(info["valid_bytes"])
    return info


def rebuild_index(path: str, index_path: str) -> dict:
    """Verify the log and write an index: one JSON object per line with
    seq/offset/length for each frame."""
    info = verify_file(path)
    with open(index_path, "w", encoding="utf-8") as fh:
        for rec in info["records"]:
            fh.write(
                json.dumps({"seq": rec["seq"], "offset": rec["offset"], "length": rec["length"]})
                + "\n"
            )
    return info


def append_frames(path: str, payloads: list[bytes]) -> dict:
    """Append frames, continuing the seq/digest chain of the existing log."""
    if os.path.exists(path) and os.path.getsize(path) > 0:
        info = verify_file(path)
        if info["status"] != CLEAN:
            raise CorruptionError(info["valid_bytes"], "existing log has incomplete tail; recover first")
        seq = info["frames"]
        prev = frame_digest(_read_frame_at(path, info["records"][-1])) if info["records"] else GENESIS_DIGEST
    else:
        seq = 0
        prev = GENESIS_DIGEST
    with open(path, "ab") as fh:
        for payload in payloads:
            frame = encode_frame(seq, payload, prev)
            fh.write(frame)
            prev = frame_digest(frame)
            seq += 1
    return {"appended": len(payloads), "next_seq": seq}


def _read_frame_at(path: str, rec: dict) -> bytes:
    with open(path, "rb") as fh:
        fh.seek(rec["offset"])
        return fh.read(rec["total"])


def inject_fault(path: str, offset: int | None, xor: int | None, truncate: int | None) -> dict:
    """Fault injection for testing: flip bytes via XOR at offset, and/or
    truncate the file to a given size."""
    with open(path, "r+b") as fh:
        if offset is not None:
            if xor is None:
                xor = 0xFF
            fh.seek(offset)
            original = fh.read(1)
            if not original:
                raise ValueError("offset beyond end of file")
            fh.seek(offset)
            fh.write(bytes([original[0] ^ xor]))
        if truncate is not None:
            fh.truncate(truncate)
    return {"size": os.path.getsize(path)}


def _print_verify(info: dict) -> None:
    print(f"status={info['status']} frames={info['frames']} "
          f"valid_bytes={info['valid_bytes']} tail_bytes={info['tail_bytes']}")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="binlog", description=__doc__.splitlines()[0])
    sub = parser.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("append", help="append frames to a log")
    p.add_argument("log")
    p.add_argument("--text", action="append", default=[], help="payload as UTF-8 text (repeatable)")
    p.add_argument("--hex", dest="hex_", action="append", default=[], help="payload as hex (repeatable)")

    p = sub.add_parser("verify", help="verify a log")
    p.add_argument("log")

    p = sub.add_parser("recover", help="truncate an incomplete tail (refuses on corruption)")
    p.add_argument("log")

    p = sub.add_parser("reindex", help="rebuild the frame index")
    p.add_argument("log")
    p.add_argument("index")

    p = sub.add_parser("inject", help="fault injection: xor a byte and/or truncate")
    p.add_argument("log")
    p.add_argument("--offset", type=int, default=None)
    p.add_argument("--xor", type=lambda s: int(s, 0), default=None)
    p.add_argument("--truncate", type=int, default=None)

    args = parser.parse_args(argv)
    try:
        if args.cmd == "append":
            payloads = [t.encode("utf-8") for t in args.text]
            payloads += [bytes.fromhex(h) for h in args.hex_]
            if not payloads:
                parser.error("append needs at least one --text or --hex")
            print(json.dumps(append_frames(args.log, payloads)))
        elif args.cmd == "verify":
            _print_verify(verify_file(args.log))
        elif args.cmd == "recover":
            _print_verify(recover_file(args.log))
        elif args.cmd == "reindex":
            _print_verify(rebuild_index(args.log, args.index))
        elif args.cmd == "inject":
            print(json.dumps(inject_fault(args.log, args.offset, args.xor, args.truncate)))
    except CorruptionError as exc:
        print(f"CORRUPT offset={exc.offset} reason={exc.reason}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
