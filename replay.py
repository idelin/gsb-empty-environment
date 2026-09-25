"""Replayable end-to-end sample of the rolling noise-floor model.

Run:  python3.11 replay.py
Deterministic: fixed fixtures + fixed shuffle seed -> identical transcript.
"""

import random

from noisefloor import TOL, TRANSIENT, NoiseFloorModel
from noisefloor import fixtures


def main():
    print("== rolling noise-floor replay ==")
    print(f"fixture: sample_rate={fixtures.SAMPLE_RATE} Hz, "
          f"hop={fixtures.HOP}, frame={fixtures.FRAME_SECONDS*1000:.0f} ms")
    print("declared latency: ingest/mark O(log W) compares, estimate O(1), "
          "no per-frame full sort")
    print(f"declared numerical tolerance: {TOL}")

    stream = fixtures.make_stream()
    model = NoiseFloorModel(window=16, quantile=0.2,
                            sample_rate=fixtures.SAMPLE_RATE,
                            hop=fixtures.HOP)

    # 1) chunked, out-of-order ingest (deterministic shuffle)
    shuffled = list(stream)
    random.Random(7).shuffle(shuffled)
    n = 0
    for i in range(0, len(shuffled), 13):
        n += model.ingest_chunk(shuffled[i:i + 13])
    print(f"\n[1] ingested {n} samples in chunks of 13 (shuffled), "
          f"version={model.version}")

    # 2) scene-scoped baselines
    for scene in fixtures.SCENES:
        est = model.estimate(fixtures.PEAK_BAND, scene)
        print(f"[2] {scene:6s}/{fixtures.PEAK_BAND}: floor={est.value:.4f} "
              f"(n_valid={est.n_valid}, v{est.version})")

    # 3) detection bound to the current version, then a late retraction
    peak_scene = fixtures.scene_of(fixtures.PEAK_FRAME, 96)
    det = model.detect(fixtures.PEAK_BAND, fixtures.PEAK_VALUE,
                       frame=fixtures.PEAK_FRAME, factor=3.0,
                       scene=peak_scene)
    print(f"[3] detect peak value={det.value} -> event={det.decision} "
          f"(baseline={det.baseline:.4f}, bound v{det.version})")
    model.mark(fixtures.PEAK_FRAME, fixtures.PEAK_BAND, TRANSIENT,
               scene=peak_scene)
    print(f"    late mark: peak frame {fixtures.PEAK_FRAME} -> transient, "
          f"version now {model.version}")
    corrections = model.correct_detections()
    for c in corrections:
        print(f"    correction det#{c.det_id}: {c.old_decision} -> "
              f"{c.new_decision} (v{c.old_version} -> v{c.new_version})")

    # 4) retracted model == never-included model
    never = NoiseFloorModel(window=16, quantile=0.2)
    never.ingest_chunk(s for s in stream
                       if not (s.frame == fixtures.PEAK_FRAME
                               and s.band == fixtures.PEAK_BAND))
    same = all(
        (lambda a, b: a.estimable == b.estimable and
         (not a.estimable or a.value == b.value))(
            model.estimate(b, s), never.estimate(b, s))
        for s in fixtures.SCENES for b in fixtures.BANDS)
    print(f"[4] baseline after retraction == never included: {same}")

    # 5) save / restore
    restored = NoiseFloorModel.from_state(model.to_state())
    ok = all(
        (lambda a, b: a.estimable == b.estimable and
         (not a.estimable or abs(a.value - b.value) <= TOL))(
            model.estimate(b, s), restored.estimate(b, s))
        for s in fixtures.SCENES for b in fixtures.BANDS)
    print(f"[5] save/restore reproduces every baseline: {ok}")

    # 6) watermark-bounded reclaim
    qid = restored.acquire_query(oldest_frame=60)
    dropped = restored.reclaim()
    restored.release_query(qid)
    dropped_all = restored.reclaim()
    print(f"[6] reclaim: {dropped} frames beyond watermark 60, "
          f"then {dropped_all} more once the query was released")


if __name__ == "__main__":
    main()
