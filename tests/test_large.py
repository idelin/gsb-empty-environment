import os
import random
import tempfile
import unittest

from pagedfa import (
    Alphabet,
    CellEdit,
    DFA,
    PageDelta,
    TableWriter,
    language_equivalent,
)
from pagedfa.patching import apply_delta, make_inverse_version


def random_dfa(seed, nstates=12, nsymbols=20, dead_state=None):
    rng = random.Random(seed)
    symbols = Alphabet([f"s{i}" for i in range(nsymbols)])
    # Build with sparse rows sharing a common default; add a few runs and
    # some explicit dead cells so all compression features are exercised.
    transitions = {}
    for q in range(nstates):
        default = rng.randrange(nstates)
        runs = []
        pos = 0
        while pos < nsymbols:
            if rng.random() < 0.55:
                length = rng.randrange(1, 4)
                hi = min(nsymbols, pos + length)
                target = rng.randrange(nstates)
                runs.append((pos, hi, target))
                pos = hi
            else:
                pos += 1
        transitions[q] = (default, runs)
    if dead_state is not None:
        transitions[dead_state] = (
            dead_state,
            [(0, nsymbols, dead_state)],
        )  # fully explicit dead row
    accepting = {q for q in range(nstates) if rng.random() < 0.3}
    return DFA(symbols, transitions, start=0, accepting=accepting,
               dead_state=dead_state)


class LargeDFATests(unittest.TestCase):
    def test_random_tables_cell_by_cell_and_page_stats(self):
        for seed in range(6):
            dfa = random_dfa(seed, nstates=10, nsymbols=17, dead_state=9)
            dense = dfa.dense_table()
            for page_size in (1, 4, 7, 16, 17):
                with self.subTest(seed=seed, page_size=page_size):
                    with tempfile.TemporaryDirectory() as directory:
                        path = os.path.join(directory, "big.pdfa")
                        with TableWriter.create(
                            path, dfa, page_size
                        ) as writer:
                            with writer.view() as v:
                                self.assertEqual(v.dense_table(), dense)
                                # localized: query one row, one page only
                                local = writer.view()
                                local.transition(3, 5)
                                snap = local.stats.snapshot()
                                self.assertEqual(snap["page_loads"], 1)
                                total_pages = (
                                    dfa.num_states
                                    * ((17 + page_size - 1) // page_size)
                                )
                                self.assertLessEqual(
                                    snap["distinct_pages"], total_pages
                                )
                                local.close()

    def test_preserving_and_changing_patch_with_inverse(self):
        dfa = random_dfa(42, nstates=8, nsymbols=13, dead_state=7)
        with tempfile.TemporaryDirectory() as directory:
            path = os.path.join(directory, "big.pdfa")
            writer = TableWriter.create(path, dfa, page_size=5)
            v1 = writer.view()

            # Language-changing patch: redirect (0, symbol 2) to an accept.
            sym_id = 2
            page, pos = divmod(sym_id, 5)
            delta = PageDelta(
                state=0, page=page,
                base_offset=v1.dir_offset(0, page),
                edits=[CellEdit(pos, 3, explicit=True)],
                accepting=dfa.accepting | {0},
            )
            new_id, new_root, inverse_payload, delta_off = apply_delta(
                writer, v1, delta, note="change"
            )

            class V:
                def __init__(self, view):
                    self.num_states = view.num_states
                    self.start = view.start
                    self.accepting = view.accepting
                    self.view = view

                def transition(self, q, s):
                    return self.view.transition(q, s)

            with writer.view(version_offset=new_root) as v2:
                same, witness = language_equivalent(
                    dfa.alphabet, dfa, V(v2)
                )
                self.assertFalse(same)
                self.assertIsNotNone(witness)
                # Old version unchanged and still reachable.
                same_old, _ = language_equivalent(
                    dfa.alphabet, dfa, V(v1)
                )
                self.assertTrue(same_old)

            # Inverse restores.
            with writer.view(version_offset=new_root) as v2:
                with open(path, "rb") as fh:
                    pass
                _rid, rroot, _, _ = make_inverse_version(
                    writer, v2,
                    _delta_bytes(writer, delta_off),
                )
            with writer.view(version_offset=rroot) as v3:
                self.assertEqual(v3.dense_table(), dfa.dense_table())
                same3, witness3 = language_equivalent(
                    dfa.alphabet, dfa, V(v3)
                )
                self.assertTrue(same3, witness3)

            v1.close()
            writer.close()


def _delta_bytes(writer, offset):
    from pagedfa.container import decode_blob_at
    import mmap
    import os
    os.lseek(writer.fd, 0, os.SEEK_END)
    size = os.lseek(writer.fd, 0, os.SEEK_CUR)
    mm = mmap.mmap(writer.fd, size, access=mmap.ACCESS_READ)
    try:
        return decode_blob_at(mm, offset)
    finally:
        mm.close()


if __name__ == "__main__":
    unittest.main()
