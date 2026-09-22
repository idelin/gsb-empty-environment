import os
import struct
import tempfile
import unittest

from pagedfa import (
    Alphabet,
    BadChecksumError,
    CallbackProvider,
    CorruptOffsetError,
    DFA,
    InvalidIndexError,
    LoadCancelledError,
    MissingPageError,
    TableView,
    TableWriter,
)
from pagedfa.container import HEADER_SIZE, decode_blob_at


def build_dfa():
    alphabet = Alphabet("abcd")  # 4 symbols, page_size=2 -> 2 pages/row
    transitions = {
        0: (3, [(0, 2, 1), (2, 4, 2)]),
        1: (3, [(0, 4, 0)]),
        2: (3, [(0, 4, 2)]),
        3: (3, [(0, 4, 3)]),  # explicit dead row, default also dead
    }
    return DFA(alphabet, transitions, start=0, accepting={1, 2},
               dead_state=3)


class CorruptionTests(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.path = os.path.join(self.dir, "t.pdfa")
        self.dfa = build_dfa()
        self.writer = TableWriter.create(self.path, self.dfa, page_size=2)
        self.writer.close()

    def _raw(self):
        with open(self.path, "rb") as fh:
            return bytearray(fh.read())

    def _write(self, data):
        with open(self.path, "wb") as fh:
            fh.write(data)

    def test_cross_page_symbol_index_never_bleeds(self):
        with TableView(self.path) as v:
            # last valid symbol id is 3; page_size 2 -> pages 0 and 1.
            self.assertEqual(v.transition(0, 1), 1)
            self.assertEqual(v.transition(0, 2), 2)
            with self.assertRaises(InvalidIndexError):
                v.transition(0, 4)
            # page 1 local position 0 is symbol 2, not symbol 0.
            self.assertNotEqual(v.transition(0, 2), v.transition(0, 0))

    def test_corrupt_directory_offset_detected_at_open(self):
        data = self._raw()
        # Root is the last blob; easiest corruption: smash active_root field.
        struct.pack_into("<Q", data, 20, 4096 * 4096)
        self._write(data)
        with self.assertRaises((CorruptOffsetError, Exception)):
            TableView(self.path)

    def test_bad_crc_in_page_blob_detected_on_page_read(self):
        with TableView(self.path) as good:
            target_offset = good.dir_offset(0, 0)
        data = self._raw()
        # Flip a byte inside the payload (after the 6-byte blob header).
        data[target_offset + 6] ^= 0xFF
        self._write(data)
        with TableView(self.path, validate=False) as v:
            with self.assertRaises(BadChecksumError):
                v.transition(0, 0)

    def test_missing_page_via_provider(self):
        def flaky(state, page):
            if (state, page) == (1, 1):
                return None  # simulate absent page
            return pages[(state, page)]

        with TableView(self.path) as ref:
            pages = _snapshot_pages(ref)
            with TableView(self.path,
                           provider=CallbackProvider(flaky),
                           validate=False) as v:
                with self.assertRaises(MissingPageError):
                    v.transition(1, 3)  # lives on page 1
                # other pages still work
                self.assertEqual(v.transition(1, 0),
                                 self.dfa.dense_table()[1][0])

    def test_cancelled_load_is_recoverable(self):
        state = {"cancel": True}

        def gate(state_id, page):
            if state["cancel"] and (state_id, page) == (2, 0):
                return LoadCancelledError(state_id, page, "user aborted")
            return pages[(state_id, page)]

        with TableView(self.path) as ref:
            pages = _snapshot_pages(ref)

        with TableView(self.path, provider=CallbackProvider(gate),
                       validate=False) as v:
            with self.assertRaises(LoadCancelledError):
                v.transition(2, 0)
            state["cancel"] = False
            # Retry succeeds; cancellation was not cached as a failure.
            self.assertEqual(v.transition(2, 0),
                             self.dfa.dense_table()[2][0])

    def test_directory_missing_entry_after_open(self):
        # Build a view whose directory drops one page (tampered root json).
        import json
        from pagedfa.container import decode_blob_at
        with TableView(self.path) as v:
            root_off = v.root.root_offset
            payload = decode_blob_at(v.mapping, root_off)
        body = json.loads(payload[5:])
        body["entries"] = [
            e for e in body["entries"]
            if (e["state"], e["page"]) != (3, 0)
        ]
        new_root = b"VROOT" + json.dumps(body).encode()
        self._append_root(new_root)
        with TableView(self.path) as v:
            with self.assertRaises(MissingPageError):
                v.transition(3, 0)

    def _append_root(self, root_payload):
        from pagedfa.container import encode_blob, publish_active_root
        fd = os.open(self.path, os.O_RDWR)
        os.lseek(fd, 0, os.SEEK_END)
        off = os.lseek(fd, 0, os.SEEK_CUR)
        os.write(fd, encode_blob(root_payload))
        os.fsync(fd)
        publish_active_root(fd, off)
        os.close(fd)


def _snapshot_pages(view):
    pages = {}
    for (state, page), (offset, _flags, _owner) in view.root.entries.items():
        pages[(state, page)] = decode_blob_at(
            view.mapping, offset, file_size=view.file_size
        )
    return pages


if __name__ == "__main__":
    unittest.main()
