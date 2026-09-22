#!/usr/bin/env python3
"""Standalone demo: paged compressed DFA transition tables.

Run with:  python3 examples/demo.py
Shows: explicit dead transitions, row sharing, cross-page intervals,
on-demand page reads, corruption / missing / cancelled page handling, a
reversible patch, language checks and a hot-swap version store.
"""

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from paged_dfa import (  # noqa: E402
    DEAD,
    DFAEncoder,
    InMemoryBackend,
    InvalidPage,
    MissingPage,
    PageLoadCancelled,
    View,
)
from paged_dfa.model import build_model, enumerate_language  # noqa: E402
from paged_dfa.patch import Edit, VersionStore, apply_delta, build_patch  # noqa: E402

ALPHABET = list(range(10))
PAGE_SIZE = 4


def build_table():
    # States 0 and 2 share a row; state 1 contains an explicit dead run that
    # crosses a page boundary when re-encoded.
    shared = [1, 1, 1, 1, 2, 2, 2, 2, 0, 0]
    return [
        shared[:],
        [0, 0, 0, 1, 1, 1, DEAD, DEAD, 0, 0],
        shared[:],
        [0] * 10,
    ]


def section(title):
    print(f"\n=== {title} ===")


def main():
    table = build_table()
    image = DFAEncoder(PAGE_SIZE).encode(ALPHABET, table, accepting={1})
    index = image.index
    print(f"states={index.state_count} distinct_rows={index.row_count} "
          f"pages={index.page_count} image_bytes={len(image.blob)}")

    section("1. cell-by-cell compressed vs source table")
    view = View(image.blob)
    mismatches = 0
    for state in range(index.state_count):
        for pos, symbol in enumerate(ALPHABET):
            if view.transition(state, symbol) != table[state][pos]:
                mismatches += 1
    print(f"mismatches over {index.state_count * len(ALPHABET)} cells: "
          f"{mismatches}; pages decoded for full scan: {view.pages_read}")

    section("2. point queries read one page, not the whole table")
    v = View(image.blob)
    print("delta(1, 6) =", v.transition(1, 6), "(explicit dead ->", DEAD, ")")
    print("pages_read after one lookup on page 1:", v.pages_read)

    section("3. failure modes stay separate")
    # corrupted page body
    damaged = bytearray(image.blob)
    e0 = index.toc[0]
    damaged[e0.offset + 1] ^= 0xFF
    try:
        View(bytes(damaged)).transition(0, 0)
    except InvalidPage:
        print("corrupted page body  -> InvalidPage")
    # corrupted index offset (TOC) refuses to open
    damaged_idx = bytearray(image.blob)
    damaged_idx[index.pages_start - 4] ^= 0xFF
    try:
        View(bytes(damaged_idx))
    except Exception as exc:
        print(f"corrupted TOC/offset -> {type(exc).__name__}: {exc}")
    # missing page
    backend = InMemoryBackend(image.blob, omit=frozenset({1}))
    miss = View(image.blob, fetcher=backend.fetch)
    try:
        miss.transition(1, 5)
    except MissingPage:
        print("missing page fetch   -> MissingPage (other pages still work:",
              miss.transition(0, 8), ")")
    # cancelled load is retryable
    gate = {"allow": False}

    def controlled(pid, offset, length):
        if pid == 0 and not gate["allow"]:
            raise PageLoadCancelled("unloaded by policy")
        return image.blob[offset:offset + length]

    gated = View(image.blob, fetcher=controlled)
    try:
        gated.transition(0, 0)
    except PageLoadCancelled:
        print("cancelled load       -> PageLoadCancelled (retryable, "
              f"read={gated.pages_read})")
    gate["allow"] = True
    gated.retry(0)
    print("after retry, delta(0,0) =", gated.transition(0, 0),
          "pages_read =", gated.pages_read)

    section("4. small reversible patch (row sharing is copy-on-write)")
    edits = [Edit(0, 5, DEAD), Edit(0, 0, 3)]
    result = build_patch(image.blob, edits)
    print("pages decoded to patch:", result.pages_read,
          "| changed page blobs:", sorted(result.delta.changed_pages))
    patched = View(result.image.blob)
    expected = [row[:] for row in table]
    for e in edits:
        expected[e.state][e.symbol] = e.target
    assert all(patched.transition(s, a) == expected[s][i]
               for s in range(index.state_count)
               for i, a in enumerate(ALPHABET))
    print("state 0 changed; shared state 2 at symbol 5 is still",
          patched.transition(2, 5), "(no pollution)")
    forward = apply_delta(image.blob, result.delta, forward=True)
    restored = apply_delta(forward, result.delta, forward=False)
    print("forward patch byte-identical:", forward == result.image.blob)
    print("inverse restores original   :", restored == image.blob)

    section("5. language before/after patch")
    old_model = index.as_model()
    new_model = patched.logical_model
    old_words = enumerate_language(old_model, max_len=3)
    new_words = enumerate_language(new_model, max_len=3)
    print(f"accepted words (<=3): before={len(old_words)} after={len(new_words)}")
    print("language unchanged after inverse:",
          enumerate_language(View(restored).logical_model, max_len=3) == old_words)

    section("6. hot swap keeps the old view on its bytes")
    store = VersionStore(image.blob)
    old_view = store.open_view()
    store.commit(result)
    current = store.open_view()
    print("old view delta(0,0) =", old_view.transition(0, 0),
          "| current delta(0,0) =", current.transition(0, 0))
    old_view.close()
    current.close()
    print("versions retained after both views close:",
          "current only" if len(store.versions()) == 1 else store.versions())


if __name__ == "__main__":
    main()
