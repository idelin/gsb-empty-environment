#!/usr/bin/env python3
"""unittest suite for binlog.  All payloads are synthetic."""

import os
import struct
import tempfile
import unittest

import binlog
from binlog import (
    CLEAN,
    TRUNCATED,
    CorruptionError,
    StreamReader,
    encode_frame,
    frame_digest,
    GENESIS_DIGEST,
    HEADER_SIZE,
    LEN_FMT,
)


def build_log(payloads):
    """Encode a valid multi-frame log, returning (bytes, boundaries)."""
    data = b""
    prev = GENESIS_DIGEST
    boundaries = [0]
    for seq, payload in enumerate(payloads):
        frame = encode_frame(seq, payload, prev)
        data += frame
        prev = frame_digest(frame)
        boundaries.append(len(data))
    return data, boundaries


class RoundTripTest(unittest.TestCase):
    def test_encode_verify_clean(self):
        data, _ = build_log([b"alpha", b"", b"synthetic-payload-3"])
        info = binlog.verify_bytes(data)
        self.assertEqual(info["status"], CLEAN)
        self.assertEqual(info["frames"], 3)
        self.assertEqual(info["valid_bytes"], len(data))
        self.assertEqual(info["tail_bytes"], 0)
        self.assertEqual([r["payload"] for r in info["records"]],
                         [b"alpha", b"", b"synthetic-payload-3"])

    def test_empty_log_is_clean(self):
        info = binlog.verify_bytes(b"")
        self.assertEqual((info["status"], info["frames"]), (CLEAN, 0))

    def test_streaming_chunk_size_invariance(self):
        data, _ = build_log([b"x" * 100, b"y", b"z" * 7])
        expected = [r["seq"] for r in binlog.verify_bytes(data)["records"]]
        for chunk in (1, 2, 3, 5, 17, 48, 4096):
            reader = StreamReader()
            got = []
            for i in range(0, len(data), chunk):
                got.extend(r["seq"] for r in reader.feed(data[i:i + chunk]))
            status, tail = reader.finish()
            self.assertEqual((status, tail), (CLEAN, 0), f"chunk={chunk}")
            self.assertEqual(got, expected, f"chunk={chunk}")


class TruncationTest(unittest.TestCase):
    def test_every_byte_truncation(self):
        """Cutting the log at EVERY byte position must classify as clean or
        truncated-tail, never corruption, and recovery must land exactly on
        the last frame boundary at or before the cut."""
        payloads = [b"aaa", b"bbbbbb", b"", b"c" * 50]
        data, boundaries = build_log(payloads)
        for cut in range(len(data) + 1):
            info = binlog.verify_bytes(data[:cut], chunk_size=7)
            if cut in boundaries:
                self.assertEqual(info["status"], CLEAN, f"cut={cut}")
            else:
                self.assertEqual(info["status"], TRUNCATED, f"cut={cut}")
            expected_valid = max(b for b in boundaries if b <= cut)
            self.assertEqual(info["valid_bytes"], expected_valid, f"cut={cut}")
            self.assertEqual(info["tail_bytes"], cut - expected_valid, f"cut={cut}")

    def test_only_length_field_written_recovers_to_previous_frame(self):
        """Acceptance: final frame has only its 4-byte length field written;
        recovery must restore the log to the end of the previous frame."""
        payloads = [b"one", b"two", b"three"]
        data, boundaries = build_log(payloads)
        partial = data + struct.pack(LEN_FMT, 1234)  # length prefix only
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "log.bin")
            with open(path, "wb") as fh:
                fh.write(partial)
            info = binlog.recover_file(path)
            self.assertEqual(info["status"], TRUNCATED)
            self.assertEqual(info["tail_bytes"], 4)
            self.assertEqual(info["valid_bytes"], boundaries[-1])
            self.assertEqual(os.path.getsize(path), boundaries[-1])
            # log is again clean and appendable
            self.assertEqual(binlog.verify_file(path)["status"], CLEAN)
            binlog.append_frames(path, [b"four"])
            final = binlog.verify_file(path)
            self.assertEqual(final["status"], CLEAN)
            self.assertEqual([r["payload"] for r in final["records"]],
                             [b"one", b"two", b"three", b"four"])


class CorruptionTest(unittest.TestCase):
    def test_crc_error_on_complete_last_frame_is_corruption(self):
        """Acceptance: a complete final frame with a bad CRC must be
        reported as corruption, NOT as an ignorable truncated tail."""
        data, _ = build_log([b"first", b"second"])
        bad = data[:-1] + bytes([data[-1] ^ 0x01])  # flip last payload byte
        with self.assertRaises(CorruptionError) as ctx:
            binlog.verify_bytes(bad)
        self.assertIn("crc32", ctx.exception.reason)
        # recovery must refuse rather than truncate the corrupt frame away
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "log.bin")
            with open(path, "wb") as fh:
                fh.write(bad)
            with self.assertRaises(CorruptionError):
                binlog.recover_file(path)
            self.assertEqual(os.path.getsize(path), len(bad))  # untouched

    def test_middle_corruption_never_skipped(self):
        """Corrupting the middle frame must stop verification there; frames
        after the corrupt one must never be committed."""
        data, boundaries = build_log([b"aa", b"bb", b"cc", b"dd"])
        mid = boundaries[1] + HEADER_SIZE  # payload of frame 1
        bad = data[:mid] + bytes([data[mid] ^ 0xFF]) + data[mid + 1:]
        with self.assertRaises(CorruptionError) as ctx:
            binlog.verify_bytes(bad)
        self.assertEqual(ctx.exception.offset, boundaries[1])
        reader = StreamReader()
        with self.assertRaises(CorruptionError):
            reader.feed(bad)
        self.assertEqual([r["seq"] for r in reader.frames], [0])  # only frame 0

    def test_implausible_length_is_corruption(self):
        data, boundaries = build_log([b"aa", b"bb"])
        pos = boundaries[1]
        bad = data[:pos] + struct.pack(LEN_FMT, binlog.MAX_PAYLOAD + 1) + data[pos + 4:]
        with self.assertRaises(CorruptionError) as ctx:
            binlog.verify_bytes(bad)
        self.assertEqual(ctx.exception.offset, pos)
        self.assertIn("length", ctx.exception.reason)

    def test_sequence_gap_is_corruption(self):
        frame0 = encode_frame(0, b"a", GENESIS_DIGEST)
        frame2 = encode_frame(2, b"b", frame_digest(frame0))  # skips seq 1
        with self.assertRaises(CorruptionError) as ctx:
            binlog.verify_bytes(frame0 + frame2)
        self.assertIn("sequence", ctx.exception.reason)

    def test_broken_prev_digest_chain_is_corruption(self):
        frame0 = encode_frame(0, b"a", GENESIS_DIGEST)
        frame1 = encode_frame(1, b"b", b"\x00" * 32)  # wrong prev digest
        with self.assertRaises(CorruptionError) as ctx:
            binlog.verify_bytes(frame0 + frame1)
        self.assertIn("prev-digest", ctx.exception.reason)

    def test_every_byte_flip_detected(self):
        """Flipping any single byte anywhere must be detected: verification
        either raises CorruptionError or classifies the log as truncated
        (a length prefix grown past EOF is indistinguishable from a torn
        write).  It must never verify as CLEAN."""
        data, _ = build_log([b"hello", b"world"])
        corrupt = truncated = 0
        for pos in range(len(data)):
            bad = data[:pos] + bytes([data[pos] ^ 0x80]) + data[pos + 1:]
            try:
                info = binlog.verify_bytes(bad)
            except CorruptionError:
                corrupt += 1
                continue
            self.assertEqual(info["status"], TRUNCATED, f"pos={pos}")
            truncated += 1
        self.assertGreater(corrupt, 0)
        self.assertEqual(corrupt + truncated, len(data))


class IndexAndCliTest(unittest.TestCase):
    def test_rebuild_index(self):
        payloads = [b"a", b"bb", b"ccc"]
        data, boundaries = build_log(payloads)
        with tempfile.TemporaryDirectory() as tmp:
            log = os.path.join(tmp, "log.bin")
            idx = os.path.join(tmp, "log.idx")
            with open(log, "wb") as fh:
                fh.write(data)
            binlog.rebuild_index(log, idx)
            with open(log, "rb") as fh:
                raw = fh.read()
            import json
            with open(idx, encoding="utf-8") as fh:
                rows = [json.loads(line) for line in fh]
            self.assertEqual([r["seq"] for r in rows], [0, 1, 2])
            for row, payload, off in zip(rows, payloads, boundaries):
                self.assertEqual(row["offset"], off)
                self.assertEqual(row["length"], len(payload))
                # index actually locates the payload in the file
                start = row["offset"] + HEADER_SIZE
                self.assertEqual(raw[start:start + row["length"]], payload)

    def test_cli_roundtrip_and_inject(self):
        with tempfile.TemporaryDirectory() as tmp:
            log = os.path.join(tmp, "log.bin")
            idx = os.path.join(tmp, "log.idx")
            self.assertEqual(binlog.main(["append", log, "--text", "hello", "--text", "world"]), 0)
            self.assertEqual(binlog.main(["verify", log]), 0)
            self.assertEqual(binlog.main(["reindex", log, idx]), 0)
            self.assertTrue(os.path.getsize(idx) > 0)
            # fault injection: corrupt one byte -> verify must fail with 1
            self.assertEqual(binlog.main(["inject", log, "--offset", "41", "--xor", "0xFF"]), 0)  # flip CRC byte of frame 0
            self.assertEqual(binlog.main(["verify", log]), 1)
            self.assertEqual(binlog.main(["recover", log]), 1)  # refuses
            # fault injection: truncate mid-frame -> recover succeeds
            self.assertEqual(binlog.main(["inject", log, "--truncate", "10"]), 0)
            self.assertEqual(binlog.main(["recover", log]), 0)
            self.assertEqual(binlog.main(["verify", log]), 0)


if __name__ == "__main__":
    unittest.main(verbosity=2)
