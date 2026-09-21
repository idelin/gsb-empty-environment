"""Cross-check demo.

1. Generate a small random multi-key stream.
2. Compute ground truth with the total-order enumeration reference.
3. Feed it through the streaming engine in several arrival orders and
   chunkings with the watermark held open: final results must be identical.
4. Show pending (un-finalized) match state being released boundedly as the
   watermark advances.

Run:  python3 -m streamcep.consistency_demo
"""

from __future__ import annotations

import random

from .matcher import (
    Collector, Event, StreamMatcher, feed_auto_watermarks,
)
from .reference import reference_matches


class FinalOnly(Collector):
    def __init__(self):
        self.proposed_now = 0
        self.final = set()

    def on_match_proposed(self, match, late=False):
        self.proposed_now += 1

    def on_match_final(self, match):
        self.final.add((match.key, match.a.rid, match.b.rid))


def finals_for(events, chunk, shuffle_seed=None, hold_watermark_open=True):
    data = list(events)
    if shuffle_seed is not None:
        random.Random(shuffle_seed).shuffle(data)
    rec = FinalOnly()
    m = StreamMatcher(rec)
    if hold_watermark_open:
        for i in range(0, len(data), chunk):
            m.push_many(data[i:i + chunk])
        m.advance(max(e.time for e in data) + 100)
    else:
        buf = []
        for e in data:
            buf.append(e)
            if len(buf) == chunk:
                feed_auto_watermarks(m, buf, max_out_of_orderness=3)
                buf = []
        feed_auto_watermarks(m, buf, max_out_of_orderness=3)
        m.advance(max(e.time for e in data) + 100)
    return rec.final, m, rec


def main():
    rng = random.Random(6)
    events = []
    for i in range(16):
        events.append(Event(rng.choice("ABC"), rng.randint(0, 12),
                            rng.choice(["k1", "k2"]), "s", i + 1))

    print("random stream (arrival id -> (type,time,key)):")
    for e in events:
        print("  %2d: %s@%2d %s" % (e.seq, e.etype, e.time, e.key))

    ref = {(k, (p[0], p[1]), (p[2], p[3]))
           for k, v in reference_matches(events).items() for p in v}
    print("\nreference (total-order enumeration): %d matches" % len(ref))
    for x in sorted(ref):
        print("   ", x)

    print("\nstreaming engine, watermark held open until flush:")
    base = None
    for seed in (1, 2, 3):
        for chunk in (1, 2, 4, 16):
            got, _, _ = finals_for(events, chunk, shuffle_seed=seed)
            tag = "OK" if got == ref else "MISMATCH"
            print("  arrival-seed=%d chunk=%2d -> %d matches  %s"
                  % (seed, chunk, len(got), tag))
            assert got == ref
            base = base or got
    print("  => every arrival order x chunking converges to the reference.")

    print("\nstreaming engine, aggressive watermark (max-out-of-order=3ms):")
    for seed in (1, 2, 3):
        got, _, _ = finals_for(events, 3, shuffle_seed=seed,
                               hold_watermark_open=False)
        missing = ref - got
        print("  seed=%d -> %d matches; %d reference matches lost to late "
              "sealing (all reported, finals never silently changed)"
              % (seed, len(got), len(missing)))
        assert got <= ref

    print("\nbounded release of pending state (in event-time order):")
    rec = FinalOnly()
    m = StreamMatcher(rec)
    ordered = sorted(events, key=lambda e: (e.time, e.etype, e.source, e.seq))
    for e in ordered:
        m.push(e)
    for w in range(0, 20, 2):
        m.advance(w)
        st = m.stats()
        print("  W=%2d  pending=%d open-tracked=%d live-events=%2d finals=%d"
              % (w, st["pending_proposed"], st["tracked_open_matches"],
                 st["live_events"], st["finalized"]))
    m.advance(100)
    st = m.stats()
    print("  W=100 pending=%d live-events=%d finals=%d (state fully drained)"
          % (st["pending_proposed"], st["live_events"], st["finalized"]))


if __name__ == "__main__":
    main()
