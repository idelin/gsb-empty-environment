"""Runnable end-to-end sample: detection, chunking, checkpointing, patching.

Run from the repo root:  python3.11 demo.py
"""

import json

import fixtures
from silseg import IncrementalSegmenter, Track, segment_tracks


def show(title, segments):
    print(title)
    for s in segments:
        print("  %s  [%5d, %5d)  dur=%d" % (s.seg_id, s.start, s.end, s.duration))
    if not segments:
        print("  (no segments)")


def main():
    params = dict(fixtures.PARAMS)

    print("== 1. Batch reference segmentation (two bursts) ==")
    ref = segment_tracks(fixtures.fixture_two_bursts(), **params)
    show("segments:", ref)

    print("\n== 2. Chunked delivery equals the batch reference ==")
    samples = fixtures.two_burst_samples()
    track = Track("main", fixtures.RATE)
    inc = IncrementalSegmenter([track], **params)
    pos = 0
    for size in (1000, 333, 80, 777, 3410):  # odd sizes, not window-aligned
        inc.add_audio("main", pos, samples[pos:pos + size])
        pos += size
    show("chunked segments:", inc.segments)
    assert [(s.start, s.end) for s in inc.segments] == [(s.start, s.end) for s in ref]
    print("chunked == batch reference: OK")

    print("\n== 3. Checkpoint during a candidate silence, then resume ==")
    track = Track("main", fixtures.RATE)
    inc = IncrementalSegmenter([track], **params)
    inc.add_audio("main", 0, samples[:1680])  # burst 1 + 1 window of silence
    state = json.loads(json.dumps(inc.checkpoint()))  # prove serializability
    restored_track = Track("main", fixtures.RATE)
    restored_track.add_samples(0, samples[:1680])
    restored = IncrementalSegmenter.restore([restored_track], state)
    inc.add_audio("main", 1680, samples[1680:])
    restored.add_audio("main", 1680, samples[1680:])
    assert [(s.start, s.end) for s in restored.segments] == \
           [(s.start, s.end) for s in ref]
    print("restored-from-checkpoint == batch reference: OK")

    print("\n== 4. Late audio merges two segments; retraction re-splits ==")
    inc = IncrementalSegmenter(fixtures.fixture_gap_bridge(), **params)
    inc.finalize()
    show("initial (gap between bursts):", inc.segments)
    events = inc.add_audio("main", 1600, fixtures.tone(440, 2400))  # fill gap
    print("patch after filling the gap:")
    for e in events:
        print("  ", e)
    show("merged:", inc.segments)
    events = inc.revise_silence(1600, 4000, is_silence=True)  # retract
    print("patch after retracting the fill:")
    for e in events:
        print("  ", e)
    show("re-split:", inc.segments)

    print("\n== 5. Export and declared latency ==")
    exported = inc.export()
    for e in exported:
        print("  export %s: %d samples/track" % (e.seg_id, e.duration))
    bounds = [(s.start, s.end) for s in inc.segments]
    assert all(bounds[i][1] <= bounds[i + 1][0] for i in range(len(bounds) - 1))
    print("no duplicated samples across exports: OK")
    print("declared latency: %d samples (%.1f ms @ %d Hz)"
          % (inc.latency_samples, 1000.0 * inc.latency_samples / fixtures.RATE,
             fixtures.RATE))

    print("\nDEMO OK")


if __name__ == "__main__":
    main()
