import os
import struct
import tempfile
import unittest
import zlib

from imagerepo.journal import (
    FRAME_HDR,
    HEADER_LEN,
    MAGIC,
    Journal,
    JournalBadMagic,
    JournalCorrupt,
    scan_journal,
)


def frame(payload: dict, rec_type=1):
    body = __import__("json").dumps(payload, sort_keys=True,
                                    separators=(",", ":")).encode()
    return FRAME_HDR.pack(zlib.crc32(body) & 0xFFFFFFFF, rec_type, len(body)) + body


class JournalTests(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.path = os.path.join(self.dir, "journal.log")
        self.journal = Journal.open(self.dir)

    def tearDown(self):
        import shutil
        shutil.rmtree(self.dir)

    def test_append_and_scan(self):
        self.journal.append({"a": 1})
        self.journal.append({"b": 2})
        records, end, truncated = scan_journal(self.path)
        self.assertEqual([r.payload for r in records], [{"a": 1}, {"b": 2}])
        self.assertFalse(truncated)

    def test_torn_tail_truncated(self):
        self.journal.append({"ok": True})
        with open(self.path, "ab") as fh:
            fh.write(frame({"torn": True})[:7])  # partial frame header+body
        records, _end, truncated = scan_journal(self.path)
        self.assertEqual(records[0].payload, {"ok": True})
        self.assertEqual(len(records), 1)
        self.assertTrue(truncated)
        # Re-scan is now clean.
        records, _end, truncated = scan_journal(self.path)
        self.assertFalse(truncated)

    def test_partial_header_garbage_tail(self):
        self.journal.append({"ok": True})
        with open(self.path, "ab") as fh:
            fh.write(b"\xff\x01")  # not zero-filled but no valid chain after
        records, _end, truncated = scan_journal(self.path)
        self.assertEqual(len(records), 1)
        self.assertTrue(truncated)

    def test_middle_corruption_rejected(self):
        good1 = frame({"n": 1})
        good2 = frame({"n": 2})
        with open(self.path, "wb") as fh:
            fh.write(MAGIC)
            fh.write(good1)
            # Corrupt a byte inside good2, but keep the remainder a valid chain
            # that resyncs at a later frame offset.
            damaged = bytearray(good2)
            damaged[FRAME_HDR.size] ^= 0xFF
            good3 = frame({"n": 3})
            fh.write(bytes(damaged) + good3)
        with self.assertRaises(JournalCorrupt):
            scan_journal(self.path)

    def test_bad_magic(self):
        with open(self.path, "wb") as fh:
            fh.write(b"X" * HEADER_LEN)
        with self.assertRaises(JournalBadMagic):
            scan_journal(self.path)

    def test_partial_magic_recovered_as_empty(self):
        with open(self.path, "wb") as fh:
            fh.write(b"\x00\x00")
        records, _end, truncated = scan_journal(self.path)
        self.assertEqual(records, [])
        self.assertFalse(truncated)

class JournalRecoveryIntegrationTests(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()

    def tearDown(self):
        import shutil
        shutil.rmtree(self.dir)

    def test_populated_repo_torn_tail_truncated_then_opens(self):
        from imagerepo import Repository
        from imagerepo.model import Ident
        repo_path = os.path.join(self.dir, "r")
        repo = Repository.open(repo_path)
        for i in range(5):
            repo.import_package(Ident(f"pkg{i}", "1", "linux-x86_64"),
                                f"payload-{i}".encode())
        del repo
        jpath = os.path.join(repo_path, "journal.log")
        with open(jpath, "ab") as fh:
            fh.write(b"\x7f" * 11)  # torn tail garbage
        reopened = Repository.open(repo_path)
        self.assertEqual(len(os.listdir(os.path.join(repo_path, "stage"))), 0)
        self.assertIsNotNone(reopened.get_metadata(Ident("pkg0", "1", "linux-x86_64")))

    def test_populated_repo_middle_corruption_refuses_recovery(self):
        from imagerepo import Repository
        from imagerepo.model import Ident
        import json as _json
        repo_path = os.path.join(self.dir, "r")
        repo = Repository.open(repo_path)
        for i in range(4):
            repo.import_package(Ident(f"pkg{i}", "1", "linux-x86_64"),
                                f"payload-{i}".encode())
        del repo
        jpath = os.path.join(repo_path, "journal.log")
        with open(jpath, "rb") as fh:
            blob = bytearray(fh.read())
        # Damage a byte in an early frame body while leaving later frames valid.
        blob[HEADER_LEN + FRAME_HDR.size + 2] ^= 0xFF
        with open(jpath, "wb") as fh:
            fh.write(blob)
        with self.assertRaises(JournalCorrupt):
            Repository.open(repo_path)

if __name__ == "__main__":
    unittest.main()
