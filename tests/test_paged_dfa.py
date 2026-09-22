"""End-to-end tests for the paged compressed DFA container."""

import os
import random
import unittest

from paged_dfa import (
    DEAD,
    DFAEncoder,
    InMemoryBackend,
    IntegrityError,
    InvalidPage,
    MissingPage,
    PageLoadCancelled,
    View,
)
from paged_dfa.codec import page_count_for
from paged_dfa.model import build_model, encode_row, enumerate_language
from paged_dfa.patch import (
    Delta,
    Edit,
    VersionStore,
    apply_delta,
    build_patch,
)
from paged_dfa.wire import digest

ALPHABET = list(range(10))
PAGE_SIZE = 4  # -> pages [0..3], [4..7], [8..9]


def sample_table():
    return [
        [2] * 10,                                  # state 0: all 2
        [2, 2, 2, 2, 0, 0, DEAD, DEAD, 0, 0],      # state 1: explicit dead run
        [1, 0, 0, 0, 0, 0, 0, 0, 3, 3],            # state 2
        [0] * 10,                                  # state 3
    ]


def encode(table, page_size=PAGE_SIZE, accepting=()):
    return DFAEncoder(page_size).encode(ALPHABET, table, accepting)


class CompressionTests(unittest.TestCase):
    def test_default_does_not_mask_explicit_dead(self):
        # Dead is the majority default but live cells must stay explicit.
        row = encode_row([DEAD, DEAD, DEAD, 3, DEAD])
        self.assertEqual(row.default, DEAD)
        self.assertEqual(row.exceptions, ((3, 1, 3),))

        # Live majority: the dead cells must survive as explicit intervals.
        row2 = encode_row([0, 0, DEAD, DEAD, 0])
        self.assertEqual(row2.default, 0)
        self.assertEqual(row2.exceptions, ((2, 2, DEAD),))

    def test_row_sharing_deduplicates_identical_rows(self):
        table = [
            [1, 1, 0, 0],
            [1, 1, 0, 0],  # identical -> shared
            [2, 2, 2, 2],
        ]
        model = build_model([0, 1, 2, 3], table)
        self.assertEqual(model.row_of[0], model.row_of[1])
        self.assertNotEqual(model.row_of[0], model.row_of[2])
        # DEAD-distinct rows must never compare equal to live ones.
        a = encode_row([DEAD, 1, 0, 0])
        b = encode_row([2, 1, 0, 0])
        self.assertFalse(a.equals_transitions(b))


class FullTableComparisonTests(unittest.TestCase):
    def test_every_cell_matches_source_table(self):
        table = sample_table()
        img = encode(table, accepting={1})
        view = View(img.blob)
        for state in range(len(table)):
            for pos, symbol in enumerate(ALPHABET):
                self.assertEqual(
                    view.transition(state, symbol), table[state][pos],
                    f"cell ({state},{symbol})",
                )
        self.assertEqual(view.page_count, page_count_for(10, PAGE_SIZE))

    def test_randomized_cell_by_cell(self):
        rng = random.Random(1234)
        states, symbols = 9, 23
        alphabet = list(range(symbols))
        table = [
            [rng.choice([0, 1, 2, 3, DEAD]) for _ in range(symbols)]
            for _ in range(states)
        ]
        img = DFAEncoder(5).encode(alphabet, table, accepting={2, 4})
        view = View(img.blob)
        for s in range(states):
            for a in range(symbols):
                self.assertEqual(view.transition(s, a), table[s][a])
        model = build_model(alphabet, table, {2, 4})
        self.assertEqual(
            enumerate_language(model, max_len=3),
            enumerate_language(view.logical_model, max_len=3),
        )


class CrossPageTests(unittest.TestCase):
    def test_interval_spanning_pages_is_split_and_queryable(self):
        # One exception run from symbol 2 to symbol 6 crosses page 0/1.
        table = [[0] * 10, [0] * 10]
        table[0][2:7] = [1] * 5
        img = encode(table)
        view = View(img.blob)
        for pos, sym in enumerate(ALPHABET):
            self.assertEqual(view.transition(0, sym), table[0][pos])
        # Reading symbols on page 0 must not require page 1.
        v = View(img.blob)
        self.assertEqual(v.transition(0, 3), 1)
        self.assertEqual(v.loaded_pages, frozenset({0}))
        self.assertEqual(v.transition(0, 5), 1)
        self.assertEqual(v.loaded_pages, frozenset({0, 1}))


class PageReadAccountingTests(unittest.TestCase):
    def test_point_lookup_reads_one_page(self):
        table = sample_table()
        img = encode(table)
        view = View(img.blob)
        self.assertEqual(view.pages_read, 0)
        view.transition(1, 9)  # page 2 only
        self.assertEqual(view.pages_read, 1)
        self.assertEqual(view.loaded_pages, frozenset({2}))
        view.transition(2, 8)  # still page 2
        self.assertEqual(view.pages_read, 1)

    def test_demand_loading_avoids_full_decompression(self):
        # 12 states x 40 symbols, page size 5 -> 8 pages; touch 2 pages only.
        states, n_sym = 12, 40
        alphabet = list(range(n_sym))
        rng = random.Random(7)
        table = [[rng.choice(range(states)) for _ in range(n_sym)]
                 for _ in range(states)]
        img = DFAEncoder(5).encode(alphabet, table)
        view = View(img.blob)
        for s in range(states):
            view.transition(s, 2)   # page 0
            view.transition(s, 17)  # page 3
        self.assertEqual(view.pages_read, 2)
        self.assertLess(view.pages_read, view.page_count)


class FaultHandlingTests(unittest.TestCase):
    def test_corrupted_index_offset_is_rejected_at_open(self):
        img = encode(sample_table())
        blob = bytearray(img.blob)
        idx = img.index
        # Flip a byte inside the TOC (a page offset/length/digest region).
        toc_corrupt = idx.pages_start - 8
        blob[toc_corrupt] ^= 0xFF
        with self.assertRaises(IntegrityError):
            View(bytes(blob))

    def test_corrupted_page_body_is_rejected_on_load_only(self):
        img = encode(sample_table())
        blob = bytearray(img.blob)
        entry = img.index.toc[0]
        # Corrupt inside page 0's body (avoid trailing checksum).
        blob[entry.offset + 1] ^= 0xFF
        view = View(bytes(blob))  # index is intact
        with self.assertRaises(InvalidPage):
            view.transition(0, 0)
        # Failure is sticky; the page is never counted as read.
        self.assertEqual(view.pages_read, 0)
        with self.assertRaises(InvalidPage):
            view.transition(0, 1)
        # An untouched page still works.
        self.assertEqual(view.transition(0, 8), 2)
        self.assertEqual(view.pages_read, 1)

    def test_missing_page(self):
        img = encode(sample_table())
        backend = InMemoryBackend(img.blob, omit=frozenset({1}))
        view = View(img.blob, fetcher=backend.fetch)
        self.assertEqual(view.transition(0, 0), 2)  # page 0 fine
        with self.assertRaises(MissingPage):
            view.transition(1, 5)  # page 1 absent
        self.assertEqual(view.pages_read, 1)
        with self.assertRaises(MissingPage):
            view.transition(1, 6)  # remembered as missing

    def test_cancelled_load_is_retryable(self):
        img = encode(sample_table())
        state = {"cancel": True}

        def fetcher(pid, offset, length):
            if pid == 0 and state["cancel"]:
                raise PageLoadCancelled("unloaded by policy")
            return img.blob[offset:offset + length]

        view = View(img.blob, fetcher=fetcher)
        with self.assertRaises(PageLoadCancelled):
            view.transition(0, 0)
        self.assertEqual(view.pages_read, 0)
        state["cancel"] = False
        view.retry(0)
        self.assertEqual(view.transition(0, 0), 2)
        self.assertEqual(view.pages_read, 1)

    def test_page_target_states_validated_before_query(self):
        # A structurally sound image whose page references state 99 must fail
        # page verification (the index has no row pointing at 99).
        from paged_dfa.codec import _build_page_blob, encode_image
        from paged_dfa.model import build_model

        alphabet = [0, 1, 2, 3]
        table = [[0, 0, 1, 0], [0, 0, 0, 0]]
        first = DFAEncoder(2).encode(alphabet, table)
        evil = _build_page_blob(0, [(0, ((0, 1, 99),))], first.index.page_salt)
        model = build_model(alphabet, table)
        e1 = first.index.toc[1]
        page1 = first.blob[e1.offset:e1.offset + e1.length]
        rebuilt = encode_image(
            model, page_size=2, tag=first.index.tag,
            page_blobs={0: evil, 1: page1},
        )
        view = View(rebuilt)  # index valid...
        with self.assertRaises(InvalidPage):  # ...but target 99 is not
            view.transition(0, 0)


class RowSharingCopyOnWriteTests(unittest.TestCase):
    def test_editing_one_shared_row_does_not_pollute_others(self):
        # States 0 and 2 start with byte-identical rows.
        shared = [1, 1, 1, 1, 2, 2, 2, 2, 0, 0]
        table = [shared[:], [0] * 10, shared[:]]
        model = build_model(ALPHABET, table)
        self.assertEqual(model.row_of[0], model.row_of[2])
        img = encode(table)

        result = build_patch(img.blob, [Edit(0, 5, DEAD)])
        view = View(result.image.blob)
        for pos, sym in enumerate(ALPHABET):
            if sym == 5:
                self.assertEqual(view.transition(0, sym), DEAD)
            else:
                self.assertEqual(view.transition(0, sym), shared[pos])
            # State 2 is untouched and must keep returning 2 at symbol 5.
            self.assertEqual(view.transition(2, sym), shared[pos], sym)

        # Only page 1 (symbols 4..7) was decoded to build the patch.
        self.assertEqual(result.pages_read, (1,))
        self.assertEqual(sorted(result.delta.changed_pages), [1])

    def test_explicit_dead_survives_patch_in_both_directions(self):
        table = sample_table()
        img = encode(table)
        # Change a default cell to dead, and an existing dead cell to live.
        result = build_patch(img.blob, [
            Edit(3, 0, DEAD),    # all-zero row gets one dead cell
            Edit(1, 6, 2),       # explicit dead cell becomes live
        ])
        view = View(result.image.blob)
        self.assertEqual(view.transition(3, 0), DEAD)
        for sym in ALPHABET[1:]:
            self.assertEqual(view.transition(3, sym), 0)
        self.assertEqual(view.transition(1, 6), 2)
        self.assertEqual(view.transition(1, 7), DEAD)  # neighbor still dead

    def test_second_patch_chains_and_unrelated_overrides_survive(self):
        table = sample_table()
        img = encode(table)
        r1 = build_patch(img.blob, [Edit(0, 1, 3)])       # touches page 0
        r2 = build_patch(r1.image.blob, [Edit(0, 6, DEAD),  # page 1, same state
                                         Edit(2, 9, 1)])  # page 2, other state
        view = View(r2.image.blob)
        self.assertEqual(view.transition(0, 1), 3)
        self.assertEqual(view.transition(0, 6), DEAD)
        self.assertEqual(view.transition(2, 9), 1)
        # Untouched cells remain intact.
        self.assertEqual(view.transition(0, 2), 2)
        self.assertEqual(view.transition(2, 0), 1)
        # r2 only decoded pages 1 and 2; the page-0 override rode along in the
        # untouched blob and did not require decoding it.
        self.assertEqual(r2.pages_read, (1, 2))


class PatchCorrectnessTests(unittest.TestCase):
    def _full_check(self, view, table):
        for state in range(len(table)):
            for pos, sym in enumerate(ALPHABET):
                self.assertEqual(view.transition(state, sym), table[state][pos])

    def test_cell_by_cell_before_and_after_patch(self):
        table = sample_table()
        img = encode(table)
        self._full_check(View(img.blob), table)
        edits = [Edit(0, 9, 1), Edit(2, 2, DEAD), Edit(1, 6, 3)]
        expected = [row[:] for row in table]
        for e in edits:
            expected[e.state][e.symbol] = e.target
        result = build_patch(img.blob, edits)
        self._full_check(View(result.image.blob), expected)
        # The delta reports exactly the pages of edited symbols.
        self.assertEqual(result.delta.changed_pages, frozenset({0, 1, 2}))

    def test_noop_edit_rebuilds_nothing(self):
        table = sample_table()
        img = encode(table)
        result = build_patch(img.blob, [Edit(0, 0, 2)])
        self.assertEqual(result.delta.page_deltas, ())
        self.assertEqual(result.image.blob, img.blob)


class DeltaReversibilityTests(unittest.TestCase):
    def test_apply_and_invert_returns_identical_bytes(self):
        table = sample_table()
        img = encode(table)
        result = build_patch(img.blob, [Edit(0, 4, DEAD), Edit(2, 8, 0)])
        forward = apply_delta(img.blob, result.delta, forward=True)
        self.assertEqual(forward, result.image.blob)
        inverse = apply_delta(forward, result.delta, forward=False)
        self.assertEqual(inverse, img.blob)

    def test_delta_roundtrip_serialization(self):
        table = sample_table()
        img = encode(table)
        result = build_patch(img.blob, [Edit(1, 6, 0)])
        raw = result.delta.serialize()
        restored = Delta.deserialize(raw)
        self.assertEqual(
            apply_delta(img.blob, restored, forward=True), result.image.blob
        )

    def test_delta_rejects_wrong_base(self):
        table = sample_table()
        img = encode(table)
        other = encode(table)  # different random tag
        result = build_patch(img.blob, [Edit(0, 0, 1)])
        with self.assertRaises(IntegrityError):
            apply_delta(other.blob, result.delta, forward=True)

    def test_delta_corruption_detected(self):
        table = sample_table()
        img = encode(table)
        result = build_patch(img.blob, [Edit(0, 0, 1)])
        raw = bytearray(result.delta.serialize())
        raw[10] ^= 0xFF
        with self.assertRaises(IntegrityError):
            Delta.deserialize(bytes(raw))


class LanguageTests(unittest.TestCase):
    def _language_dfa(self):
        # Binary alphabet {0,1}; accepts strings ending in '1'.
        # state 0 = no trailing 1 (start/nonaccepting), 1 = trailing 1.
        alphabet = [0, 1]
        table = [
            [0, 1],
            [0, 1],
        ]
        return DFAEncoder(1).encode(alphabet, table, accepting={1})

    def test_language_matches_before_and_after_patch(self):
        img = self._language_dfa()
        old_view = View(img.blob)
        # Flip transition (1 on 1) so state 1 returns to 0: language becomes
        # words whose only '1' is the final symbol ("0*1").
        result = build_patch(img.blob, [Edit(1, 1, 0)])
        new_view = View(result.image.blob)

        old_model = img.index.as_model()
        new_model = new_view.logical_model

        def words(view, n):
            out = set()
            def rec(state, word, depth):
                if state in view.index.accepting:
                    out.add(word)
                if depth == n:
                    return
                for sym in (0, 1):
                    rec(view.transition(state, sym), word + (sym,), depth + 1)
            rec(0, (), 0)
            return out

        self.assertEqual(words(old_view, 5),
                         enumerate_language(old_model, max_len=5))
        new_words = words(new_view, 5)
        self.assertEqual(new_words,
                         enumerate_language(new_model, max_len=5))
        for w in new_words:
            self.assertIn(1, w)
            self.assertTrue(w[-1] == 1)
        # Concretely, a second 1 is allowed but a word ending in 0 is rejected.
        self.assertIn((1, 1, 1), new_words)
        self.assertNotIn((1, 0), new_words)
        # Old view keeps the pre-patch language.
        self.assertIn((1, 1), words(old_view, 2))
        self.assertNotIn((1, 1), new_words)


class VersionStoreTests(unittest.TestCase):
    def test_hot_swap_keeps_old_view_referenced(self):
        table = sample_table()
        img = encode(table)
        store = VersionStore(img.blob)
        old_view = store.open_view()
        old_id = store.current.version_id

        result = build_patch(img.blob, [Edit(0, 0, DEAD)])
        new_version = store.commit(result)
        self.assertNotEqual(new_version.version_id, old_id)

        # New current observes the patch; the old view sees the old table.
        new_view = store.open_view()
        self.assertEqual(new_view.transition(0, 0), DEAD)
        self.assertEqual(old_view.transition(0, 0), 2)

        # Old version retained while the old view is open, gone after close.
        self.assertIn(old_id, store.versions())
        old_view.close()
        self.assertNotIn(old_id, store.versions())

    def test_rollback_via_inverse_delta(self):
        table = sample_table()
        img = encode(table)
        store = VersionStore(img.blob)
        result = build_patch(img.blob, [Edit(2, 0, DEAD)])
        new_blob = store.commit(result).blob
        restored = apply_delta(new_blob, result.delta, forward=False)
        self.assertEqual(restored, img.blob)
        store.commit_blob(restored)
        view = store.open_view()
        self.assertEqual(view.transition(2, 0), 1)


class QueryValidationTests(unittest.TestCase):
    def test_bad_state_and_symbol(self):
        view = View(encode(sample_table()).blob)
        with self.assertRaises(ValueError):
            view.transition(99, 0)
        with self.assertRaises(ValueError):
            view.transition(0, 42)
        with self.assertRaises(ValueError):
            view.transition(0, -1)

    def test_builder_rejects_unknown_target(self):
        with self.assertRaises(ValueError):
            build_model([0, 1], [[7, 0]])

    def test_patch_rejects_invalid_edit(self):
        img = encode(sample_table())
        with self.assertRaises(ValueError):
            build_patch(img.blob, [Edit(0, 99, 0)])
        with self.assertRaises(ValueError):
            build_patch(img.blob, [Edit(0, 0, 99)])
        with self.assertRaises(ValueError):
            build_patch(img.blob, [Edit(99, 0, 0)])


class TruncationTests(unittest.TestCase):
    def test_truncated_container_does_not_open(self):
        img = encode(sample_table())
        with self.assertRaises(IntegrityError):
            View(img.blob[: len(img.blob) - 3])
        with self.assertRaises(IntegrityError):
            View(img.blob[:5])


class CorruptedOffsetTests(unittest.TestCase):
    def test_toc_offset_tamper_never_opens(self):
        img = encode(sample_table())
        blob = bytearray(img.blob)
        # First byte of the TOC region is the page count varint; flipping it
        # changes the index digest and can never open as a valid container.
        toc_start = img.index.pages_start - (
            1 + sum(1 + 2 + 16 for _ in img.index.toc)
        )
        # Simpler and exact: recompute TOC start by parsing is unnecessary;
        # mutate a byte known to sit inside the TOC region.
        from paged_dfa.patch import _h1_end, _skip_header2
        h1 = _h1_end(img.blob, img.index)
        toc_start = _skip_header2(img.blob, h1)
        blob[toc_start + 1] ^= 0x01
        with self.assertRaises(IntegrityError):
            View(bytes(blob))

    def test_page_digest_tamper_is_separate_from_body(self):
        img = encode(sample_table())
        blob = bytearray(img.blob)
        e0 = img.index.toc[0]
        blob[e0.offset + e0.length - 1] ^= 0xFF
        view = View(bytes(blob))
        with self.assertRaises(InvalidPage):
            view.transition(0, 0)


class FullMaterializationTests(unittest.TestCase):
    def test_logical_model_matches_view_after_chain_of_patches(self):
        table = sample_table()
        blob = encode(table).blob
        r1 = build_patch(blob, [Edit(0, 2, DEAD)])
        r2 = build_patch(r1.image.blob, [Edit(0, 9, 1), Edit(2, 3, DEAD)])
        view = View(r2.image.blob)
        materialized = view.logical_model
        expected = [row[:] for row in table]
        expected[0][2] = DEAD
        expected[0][9] = 1
        expected[2][3] = DEAD
        for s in range(len(table)):
            for i, sym in enumerate(ALPHABET):
                self.assertEqual(view.transition(s, sym), expected[s][i])
                self.assertEqual(materialized.transition(s, sym), expected[s][i])


if __name__ == "__main__":
    unittest.main(verbosity=2)
