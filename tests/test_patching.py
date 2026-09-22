import os
import tempfile
import unittest

from pagedfa import (
    Alphabet,
    CellEdit,
    DFA,
    DefaultEdit,
    PageDelta,
    TableWriter,
    language_equivalent,
)
from pagedfa.container import decode_blob_at
from pagedfa.patching import apply_delta, make_inverse_version, parse_delta_payload


def build_dfa():
    alphabet = Alphabet("abc")
    # 3 states; row 0 == row 1 (shared), row 2 explicit dead everywhere.
    transitions = {
        0: (2, [(0, 2, 0), (2, 3, 1)]),  # a,b -> 0 ; c -> 1 ; default dead
        1: (2, [(0, 2, 0), (2, 3, 1)]),  # identical to row 0
        2: (2, [(0, 3, 2)]),             # explicit dead transitions
    }
    return DFA(alphabet, transitions, start=0, accepting={1}, dead_state=2)


class PatchingTests(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.path = os.path.join(self.dir, "t.pdfa")
        self.dfa = build_dfa()
        self.writer = TableWriter.create(self.path, self.dfa, page_size=2)
        self.v1 = self.writer.view()

    def tearDown(self):
        self.v1.close()
        self.writer.close()

    def test_patch_then_inverse_restores_language_and_cells(self):
        alphabet = self.dfa.alphabet
        before = self.dfa.dense_table()

        # Language-changing edit on state 0 page 0: symbol 'a' now -> 1.
        delta = PageDelta(
            state=0, page=0,
            base_offset=self.v1.dir_offset(0, 0),
            edits=[CellEdit(0, 1, explicit=True)],
            accepting=frozenset({1}),
        )
        new_id, new_root, inverse_payload, delta_off = apply_delta(
            self.writer, self.v1, delta, note="flip 0,a"
        )

        with self.writer.view(version_offset=new_root) as v2:
            self.assertEqual(v2.transition(0, 0), 1)
            changed_dfa = _ViewDFA(alphabet, v2)
            same, witness = language_equivalent(alphabet, self.dfa, changed_dfa)
            self.assertFalse(same)
            self.assertIsNotNone(witness)

            # Old view keeps answering old data.
            self.assertEqual(self.v1.transition(0, 0), 0)

        # Inverse patch restores exact cells and language.
        with self.writer.view(version_offset=new_root) as v2:
            rid, rroot, _, _ = make_inverse_version(
                self.writer, v2,
                _read_delta(self.writer, delta_off),
                note="rollback",
            )
        with self.writer.view(version_offset=rroot) as v3:
            self.assertEqual(v3.dense_table(), before)
            restored = _ViewDFA(alphabet, v3)
            same, witness = language_equivalent(
                alphabet, self.dfa, restored
            )
            self.assertTrue(same, witness)

    def test_copy_on_write_shared_row_not_polluted(self):
        # Modify shared row 0; row 1 must remain identical to the original.
        original_row1 = self.v1.dense_table()[1]
        delta = PageDelta(
            state=0, page=0,
            base_offset=self.v1.dir_offset(0, 0),
            edits=[CellEdit(1, 1, explicit=True)],
        )
        _id, new_root, _inv, _doff = apply_delta(self.writer, self.v1, delta)
        with self.writer.view(version_offset=new_root) as v2:
            self.assertEqual(v2.transition(0, 1), 1)
            self.assertEqual(v2.dense_table()[1], original_row1)
            # Blobs differ: sharing detached on write.
            self.assertNotEqual(
                v2.dir_offset(0, 0), v2.dir_offset(1, 0)
            )

    def test_default_change_keeps_explicit_dead(self):
        # Row 2 default == dead; change default to 0: explicit dead survives.
        delta = PageDelta(
            state=2, page=0,
            base_offset=self.v1.dir_offset(2, 0),
            edits=[DefaultEdit(0)],
        )
        _id, new_root, _inv, _ = apply_delta(self.writer, self.v1, delta)
        with self.writer.view(version_offset=new_root) as v2:
            self.assertTrue(v2.is_explicit(2, 0))
            self.assertEqual(v2.transition(2, 0), 2)
            # symbol at pos 1 is also explicit (second cell of run).
            self.assertTrue(v2.is_explicit(2, 1))
            self.assertEqual(v2.transition(2, 1), 2)

    def test_delta_stale_base_rejected(self):
        delta = PageDelta(
            state=0, page=0, base_offset=99999,
            edits=[CellEdit(0, 1)],
        )
        with self.assertRaises(Exception):
            apply_delta(self.writer, self.v1, delta)


class _ViewDFA:
    """Adapt a TableView to the language_equivalent interface."""

    def __init__(self, alphabet, view):
        self.alphabet = alphabet
        self.view = view
        self.num_states = view.num_states
        self.start = view.start
        self.accepting = view.accepting

    def transition(self, q, sym_id):
        return self.view.transition(q, sym_id)


def _read_delta(writer, offset):
    return decode_blob_at(_fd_mmap(writer), offset)


def _fd_mmap(writer):
    import mmap, os
    os.lseek(writer.fd, 0, os.SEEK_END)
    size = os.lseek(writer.fd, 0, os.SEEK_CUR)
    mm = mmap.mmap(writer.fd, size, access=mmap.ACCESS_READ)
    return mm


if __name__ == "__main__":
    unittest.main()
