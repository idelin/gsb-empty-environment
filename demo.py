"""Runnable demo for the unbiased_shuffle toolkit (Python 3.11, stdlib only).

Every section is labeled [EXACT] (proven by exhaustive enumeration or by
construction) or [EMPIRICAL] (statistical estimate on sampled data).
"""

import collections
import itertools
import json
import math
import os
import tempfile

from unbiased_shuffle import (
    BlockedRecordStore,
    ExternalShuffler,
    PausableShuffle,
    SimulatedCrash,
    SplitMix64Source,
    fisher_yates,
    num_partial_permutations,
    rank_partial,
    unrank_partial,
)


def section(title):
    print("\n" + "=" * 68)
    print(title)
    print("=" * 68)


def demo_pausable_shuffle():
    section("1. Pausable Fisher-Yates with caller-injected PRNG")
    rng = SplitMix64Source(seed=20261001)
    sh = PausableShuffle(list("ABCDEFGH"), rng)
    while not sh.done:
        i = sh.frontier
        sh.step()
        print(f"  frontier i={i}  j={sh.trace[-1][1]} (range [0, {i}])  "
              f"-> {''.join(sh.items)}")
    print(f"  result: {''.join(sh.items)}  seed={rng.seed}  "
          f"samples={sh.samples}  raw 64-bit draws={rng.draws}")
    # Pause / resume via snapshot.
    rng2 = SplitMix64Source(seed=20261001)
    paused = PausableShuffle(list("ABCDEFGH"), rng2)
    paused.run(max_steps=3)
    snap = paused.snapshot()
    resumed = PausableShuffle.restore(snap, SplitMix64Source(seed=0))
    resumed.run()
    print(f"  paused after 3 swaps, restored from snapshot: "
          f"{''.join(resumed.items)} (identical: {resumed.items == sh.items})")


def demo_unrank():
    section("2. Partial permutation positioned by rank  [EXACT]")
    n, k = 5, 3
    total = num_partial_permutations(n, k)
    print(f"  P({n},{k}) = {total}")
    for r in (0, 17, total - 1):
        perm = unrank_partial(n, k, r)
        print(f"  rank {r:>2} -> {perm}  (rank again: {rank_partial(n, k, perm)})")
    big = num_partial_permutations(1000, 500)
    print(f"  big-integer check: P(1000,500) has {len(str(big))} digits; "
          f"roundtrip ok: "
          f"{rank_partial(1000, 500, unrank_partial(1000, 500, big // 2)) == big // 2}")


def demo_exact_enumeration():
    section("3. Small-n exhaustive enumeration  [EXACT]")
    for n in (3, 4, 5):
        counts = collections.Counter()
        choice_ranges = [range(i + 1) for i in range(n - 1, 0, -1)]
        for draws in itertools.product(*choice_ranges):
            seq = list(draws)
            src = _Scripted(seq)
            counts[tuple(PausableShuffle(list(range(n)), src).run())] += 1
        print(f"  n={n}: {math.prod(len(r) for r in choice_ranges)} draw sequences "
              f"-> {len(counts)} distinct permutations, "
              f"each exactly once: {all(c == 1 for c in counts.values())}")
    print("  => every permutation has probability exactly 1/n! (proven by")
    print("     enumeration, not estimated)")


class _Scripted:
    def __init__(self, values):
        self.values = values
        self.pos = 0

    def randbelow(self, n):
        v = self.values[self.pos]
        self.pos += 1
        return v


def demo_external_shuffle():
    section("4. Resumable external-memory shuffle with crash recovery")
    tmp = tempfile.mkdtemp(prefix="shuffle_demo_")
    data = os.path.join(tmp, "data.bin")
    meta = os.path.join(tmp, "meta")
    os.makedirs(meta)
    n, rec_size, seed = 20, 8, 777
    original = [i.to_bytes(rec_size, "big") for i in range(n)]
    with open(data, "wb") as fh:
        fh.write(b"".join(original))

    def fresh(block_size, hook=None):
        store = BlockedRecordStore(data, rec_size, n, block_size=block_size)
        return ExternalShuffler(store, SplitMix64Source(seed), seed, meta, hook), store

    # Reference: uninterrupted run.
    ref, store = fresh(block_size=1)
    ref.run()
    reference = store.read_all()
    store.close()
    print(f"  continuous run (block_size=1): "
          f"{[int.from_bytes(r, 'big') for r in reference]}")

    # Reset, then crash mid-shuffle and recover.
    with open(data, "wb") as fh:
        fh.write(b"".join(original))
    for name in os.listdir(meta):
        os.remove(os.path.join(meta, name))

    def crash_at(phase, frontier):
        if phase == "after_journal" and frontier == 12:
            raise SimulatedCrash("power loss")

    crashed, store = fresh(block_size=4, hook=crash_at)
    try:
        crashed.run()
    except SimulatedCrash as exc:
        print(f"  simulated crash: {exc} at frontier 12 (block_size=4)")
    store.close()
    with open(os.path.join(meta, "journal.json")) as fh:
        journal = json.load(fh)
    print(f"  journal on disk: frontier={journal['frontier']} j={journal['j']} "
          f"(pending swap, full record images stored)")
    recovered, store = fresh(block_size=4)
    recovered.run()
    result = store.read_all()
    store.close()
    print(f"  recovered run:   {[int.from_bytes(r, 'big') for r in result]}")
    print(f"  identical to continuous run: {result == reference}")
    print(f"  still a permutation of the input: {sorted(result) == original}")
    with open(os.path.join(meta, "checkpoint.json")) as fh:
        cp = json.load(fh)
    print(f"  final checkpoint: frontier={cp['frontier']} "
          f"published_from={cp['published_from']} seed={cp['seed']} "
          f"samples={cp['samples']}")


def demo_empirical():
    section("5. Permutation frequencies, n=3  [EMPIRICAL estimate]")
    trials = 120_000
    rng = SplitMix64Source(seed=2026)
    counts = collections.Counter(
        tuple(fisher_yates(range(3), rng)) for _ in range(trials)
    )
    for perm, c in sorted(counts.items()):
        print(f"  {perm}: {c}  ({c / trials:.4%})")
    expected = trials / 6
    chi2 = sum((c - expected) ** 2 / expected for c in counts.values())
    print(f"  chi2={chi2:.3f} (df=5, 1% critical 15.086) -- empirical estimate")
    print(f"  seed={rng.seed}  samples={trials * 2}  raw draws={rng.draws}")


if __name__ == "__main__":
    demo_pausable_shuffle()
    demo_unrank()
    demo_exact_enumeration()
    demo_external_shuffle()
    demo_empirical()
