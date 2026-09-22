import os
import tempfile
import unittest

from pagedfa import (
    Alphabet,
    BadChecksumError,
    BadTargetError,
    CallbackProvider,
    CellEdit,
    CorruptOffsetError,
    DFA,
    DefaultEdit,
    InvalidIndexError,
    LoadCancelledError,
    MissingPageError,
    PageDelta,
    TableView,
    TableWriter,
    apply_delta,
    language_equivalent,
)
from pagedfa.chunks import decode_chunk, encode_chunk
from pagedfa.encoder import Encoder


def words_end_with_b():
    alphabet = Alphabet("ab")
    transitions = {
        0: (2, [(0, 1, 0), (1, 2, 1)]),
        1: (2, [(0, 1, 0), (1, 2, 1)]),
        2: (2, [(0, 2, 2)]),
    }
    return DFA(alphabet, transitions, start=0, accepting={1}, dead_state=2)


class TempFileMixin:
    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.path = os.path.join(self.dir, "table.pdfa")


class ChunkTests(unittest.TestCase):
    def test_round_trip_with_explicit_default_value(self):
        cells = [2, 0, 1, 2]
        sticky = [True, False, False, False]  # explicit dead at pos 0
        payload = encode_chunk(cells, default=2, sticky=sticky)
        chunk = decode_chunk(payload)
        self.assertEqual(chunk.cells(), cells)
        self.assertTrue(chunk.is_sticky(0))
        self.assertFalse(chunk.is_sticky(3))  # implicit default, not explicit

    def test_adjacent_different_targets_not_merged(self):
        cells = [1, 2, 1]
        sticky = [True, True, False]
        payload = encode_chunk(cells, default=1, sticky=sticky)
        chunk = decode_chunk(payload)
        self.assertEqual(chunk.runs, [(0, 1, 1), (1, 2, 2)])


class EncoderQueryTests(TempFileMixin, unittest.TestCase):
    def test_full_table_cell_by_cell_multiple_page_sizes(self):
        dfa = words_end_with_b()
        for page_size in (1, 2, 3):
            with self.subTest(page_size=page_size):
                with TableWriter.create(self.path, dfa, page_size) as w:
                    with w.view() as v:
                        dense = dfa.dense_table()
                        got = v.dense_table()
                        self.assertEqual(got, dense)
                        for q in range(dfa.num_states):
                            for s in range(len(dfa.alphabet)):
                                self.assertEqual(
                                    v.transition(q, s), dense[q][s]
                                )

    def test_explicit_dead_not_masked_by_dead_default(self):
        dfa = words_end_with_b()
        with TableWriter.create(self.path, dfa, page_size=2) as w:
            with w.view() as v:
                # Row 2 default points at dead; both cells are explicit dead.
                self.assertTrue(v.is_explicit(2, 0))
                self.assertTrue(v.is_explicit(2, 1))
                self.assertEqual(v.transition(2, 0), 2)

    def test_row_sharing_detected(self):
        dfa = words_end_with_b()  # rows 0 and 1 identical
        plan, report, _ = Encoder(dfa, page_size=2).plan()
        self.assertEqual(report.shared_rows, 1)
        with TableWriter.create(self.path, dfa, page_size=2) as w:
            with w.view() as v:
                self.assertEqual(v.canonical_owner(1, 0), 0)
                self.assertEqual(
                    v.dir_offset(1, 0), v.dir_offset(0, 0)
                )

    def test_index_validation_before_query(self):
        dfa = words_end_with_b()
        with TableWriter.create(self.path, dfa, page_size=2) as w:
            with w.view() as v:
                with self.assertRaises(InvalidIndexError):
                    v.transition(9, 0)
                with self.assertRaises(InvalidIndexError):
                    v.transition(0, 7)

    def test_bad_target_rejected_on_input(self):
        alphabet = Alphabet("ab")
        with self.assertRaises(BadTargetError):
            DFA(alphabet, {0: (5, [])}, start=0, accepting=set())


class StatsTests(TempFileMixin, unittest.TestCase):
    def test_localized_query_reads_only_touched_pages(self):
        alphabet = Alphabet("abcdefgh")  # 8 symbols
        rows = {q: (0, [(i, i + 1, (q + i) % 4) for i in range(8)])
                for q in range(4)}
        dfa = DFA(alphabet, rows, start=0, accepting={0})
        with TableWriter.create(self.path, dfa, page_size=2) as w:
            with w.view() as v:
                v.transition(0, 0)
                v.transition(0, 1)  # same page -> hit
                v.transition(0, 2)  # second page -> load
                snap = v.stats.snapshot()
                self.assertEqual(snap["page_loads"], 2)
                self.assertEqual(snap["page_hits"], 1)
                self.assertLess(snap["distinct_pages"], 4 * 4)
                total_pages = 4 * 4
                self.assertLess(snap["distinct_pages"], total_pages)


if __name__ == "__main__":
    unittest.main()
