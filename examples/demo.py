#!/usr/bin/env python3
"""Independent, runnable demonstration of every guarantee.

Usage::

    python3.11 examples/demo.py [scenario ...]

Scenarios: encode, dead, sharing, cross-page, corrupt-offset, bad-crc,
missing-page, cancel, hotswap, stats.  With no argument every scenario runs.
Each scenario is self-contained and exits non-zero if its invariant breaks.
"""

import json
import os
import struct
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from pagedfa import (
    Alphabet,
    BadChecksumError,
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
    language_equivalent,
)
from pagedfa.container import decode_blob_at
from pagedfa.patching import apply_delta, make_inverse_version

PASS = 0


def check(label, condition, detail=""):
    global PASS
    status = "PASS" if condition else "FAIL"
    if condition:
        PASS += 1
    print(f"[{status}] {label}" + (f" -- {detail}" if detail else ""))
    if not condition:
        raise SystemExit(f"counterexample violated: {label}")


def sample_dfa():
    """Language: words over a,b ending in b.  State 2 is the dead state."""
    alphabet = Alphabet("ab")
    transitions = {
        0: (2, [(0, 1, 0), (1, 2, 1)]),
        1: (2, [(0, 1, 0), (1, 2, 1)]),  # identical row -> shareable
        2: (2, [(0, 2, 2)]),              # explicit dead on both symbols
    }
    return DFA(alphabet, transitions, start=0, accepting={1}, dead_state=2)


def scenario_encode(path):
    dfa = sample_dfa()
    with TableWriter.create(path, dfa, page_size=2) as writer:
        report = writer._last_report
        with writer.view() as v:
            compressed = v.dense_table()
    check("compressed table equals explicit table",
          compressed == dfa.dense_table(), json.dumps(compressed))
    check("identical rows share storage",
          report.shared_rows == 1, report.summary())


def scenario_dead(path):
    dfa = sample_dfa()
    with TableWriter.create(path, dfa, page_size=2) as writer:
        with writer.view() as v:
            before = v.transition(2, 0)
            explicit_before = v.is_explicit(2, 0)
            delta = PageDelta(
                state=2, page=0, base_offset=v.dir_offset(2, 0),
                edits=[DefaultEdit(0)],
            )
            _id, root, _inv, _ = apply_delta(writer, v, delta)
            with writer.view(version_offset=root) as v2:
                check("explicit dead survives a default change",
                      v2.is_explicit(2, 0) and v2.transition(2, 0) == 2
                      and before == 2 and explicit_before)


def scenario_sharing(path):
    dfa = sample_dfa()
    with TableWriter.create(path, dfa, page_size=2) as writer:
        with writer.view() as v1:
            original_row1 = v1.dense_table()[1]
            delta = PageDelta(
                state=0, page=0, base_offset=v1.dir_offset(0, 0),
                edits=[CellEdit(0, 1, explicit=True)],
            )
            _id, root, _inv, _ = apply_delta(writer, v1, delta)
            with writer.view(version_offset=root) as v2:
                check("editing one shared row does not touch the other",
                      v2.dense_table()[1] == original_row1
                      and v2.transition(0, 0) == 1)


def scenario_cross_page(path):
    alphabet = Alphabet("abcd")
    dfa = DFA(
        alphabet,
        {q: (0, [(s, s + 1, (q + s) % 4) for s in range(4)])
         for q in range(4)},
        start=0, accepting={0},
    )
    with TableWriter.create(path, dfa, page_size=2) as writer:
        with writer.view() as v:
            raised = False
            try:
                v.transition(0, 4)
            except InvalidIndexError:
                raised = True
            check("cross-page symbol index rejected",
                  raised and v.transition(0, 2) == dfa.dense_table()[0][2])


def _rewrite(path, data):
    with open(path, "wb") as fh:
        fh.write(data)


def scenario_corrupt_offset(path):
    dfa = sample_dfa()
    with TableWriter.create(path, dfa, page_size=2):
        pass
    with open(path, "rb") as fh:
        data = bytearray(fh.read())
    struct.pack_into("<Q", data, 20, 10_000_000)
    _rewrite(path, data)
    raised = False
    try:
        TableView(path)
    except CorruptOffsetError:
        raised = True
    check("corrupt root offset rejected before queries", raised)


def scenario_bad_crc(path):
    dfa = sample_dfa()
    with TableWriter.create(path, dfa, page_size=2) as writer:
        with writer.view() as v:
            offset = v.dir_offset(2, 0)
    with open(path, "rb") as fh:
        data = bytearray(fh.read())
    data[offset + 6] ^= 0xFF
    _rewrite(path, data)
    raised = False
    try:
        with TableView(path, validate=False) as v:
            v.transition(2, 0)
    except BadChecksumError:
        raised = True
    check("per-page CRC catches a flipped payload byte", raised)


def scenario_missing_page(path):
    dfa = sample_dfa()
    with TableWriter.create(path, dfa, page_size=1) as writer:
        with writer.view() as ref:
            payloads = {}
            for (s, p), (off, _f, _o) in ref.root.entries.items():
                payloads[(s, p)] = decode_blob_at(
                    ref.mapping, off, file_size=ref.file_size
                )

    def provider(state, page):
        if (state, page) == (2, 1):
            return None
        return payloads[(state, page)]

    with TableView(path, provider=CallbackProvider(provider),
                   validate=False) as v:
        raised = False
        try:
            v.transition(2, 1)
        except MissingPageError:
            raised = True
        check("missing page is isolated; resident pages still answer",
              raised and v.transition(0, 0) == 0)


def scenario_cancel(path):
    dfa = sample_dfa()
    with TableWriter.create(path, dfa, page_size=2) as writer:
        with writer.view() as ref:
            payloads = {}
            for (s, p), (off, _f, _o) in ref.root.entries.items():
                payloads[(s, p)] = decode_blob_at(
                    ref.mapping, off, file_size=ref.file_size
                )
    gate = {"cancelled": True}

    def provider(state, page):
        if gate["cancelled"] and (state, page) == (1, 0):
            return LoadCancelledError(state, page, "demo abort")
        return payloads[(state, page)]

    with TableView(path, provider=CallbackProvider(provider),
                   validate=False) as v:
        raised = False
        try:
            v.transition(1, 0)
        except LoadCancelledError:
            raised = True
        gate["cancelled"] = False
        check("cancelled load is retryable, not cached as failure",
              raised and v.transition(1, 0) == 0)


def scenario_hotswap(path):
    dfa = sample_dfa()
    with TableWriter.create(path, dfa, page_size=2) as writer:
        old = writer.view()
        delta = PageDelta(
            state=0, page=0, base_offset=old.dir_offset(0, 0),
            edits=[CellEdit(0, 1, explicit=True)],
        )
        _id, new_root, _inv, delta_off = apply_delta(writer, old, delta)
        new = writer.view(version_offset=new_root)

        class Adapter:
            def __init__(self, view):
                self.num_states = view.num_states
                self.start = view.start
                self.accepting = view.accepting
                self.view = view

            def transition(self, q, s):
                return self.view.transition(q, s)

        same, witness = language_equivalent(
            dfa.alphabet, _DFAAdapter(dfa), Adapter(new),
        )
        _rid, restore_root, _, _ = make_inverse_version(
            writer, new, _read(writer, delta_off)
        )
        restored = writer.view(version_offset=restore_root)
        check("patch changes the language", not same and witness is not None,
              f"witness symbol ids={witness}")
        check("old view keeps serving the old version",
              old.transition(0, 0) == 0 and new.transition(0, 0) == 1)
        check("inverse patch restores every cell",
              restored.dense_table() == dfa.dense_table())
        for view in (old, new, restored):
            view.close()


class _DFAAdapter:
    def __init__(self, dfa):
        self.num_states = dfa.num_states
        self.start = dfa.start
        self.accepting = dfa.accepting
        self.dfa = dfa

    def transition(self, q, s):
        return self.dfa.dense_table()[q][s]


def scenario_stats(path):
    alphabet = Alphabet([f"x{i}" for i in range(12)])
    dfa = DFA(
        alphabet,
        {q: (q, [(s, s + 1, (q * 3 + s) % 6) for s in range(12)])
         for q in range(6)},
        start=0, accepting={0},
    )
    with TableWriter.create(path, dfa, page_size=3) as writer:
        with writer.view() as v:
            total = v.num_states * v.pages_per_state
            v.transition(2, 4)
            v.transition(2, 5)
            snap = v.stats.snapshot()
    check("reads touch only accessed pages (no full decompression)",
          snap["page_loads"] == 1 and snap["page_hits"] == 1
          and total == 24, f"stats={snap}, total_pages={total}")


def _read(writer, offset):
    import mmap
    os.lseek(writer.fd, 0, os.SEEK_END)
    size = os.lseek(writer.fd, 0, os.SEEK_CUR)
    mm = mmap.mmap(writer.fd, size, access=mmap.ACCESS_READ)
    try:
        return decode_blob_at(mm, offset)
    finally:
        mm.close()


SCENARIOS = {
    "encode": scenario_encode,
    "dead": scenario_dead,
    "sharing": scenario_sharing,
    "cross-page": scenario_cross_page,
    "corrupt-offset": scenario_corrupt_offset,
    "bad-crc": scenario_bad_crc,
    "missing-page": scenario_missing_page,
    "cancel": scenario_cancel,
    "hotswap": scenario_hotswap,
    "stats": scenario_stats,
}


def main(argv):
    names = argv[1:] or list(SCENARIOS)
    for name in names:
        print(f"\n=== scenario: {name} ===")
        with tempfile.TemporaryDirectory() as directory:
            SCENARIOS[name](os.path.join(directory, "demo.pdfa"))
    print(f"\nall {PASS} checks passed")


if __name__ == "__main__":
    main(sys.argv)
