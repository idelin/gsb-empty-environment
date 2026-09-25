"""Equivalence against the full batch reference: chunking, gaps, checkpoints."""

import json
import unittest

import fixtures
from silseg import IncrementalSegmenter, Track, segment_tracks


def bounds(segments):
    return [(s.start, s.end) for s in segments]


def feed_in_chunks(samples, chunk_sizes, **params):
    track = Track("main", fixtures.RATE)
    inc = IncrementalSegmenter([track], **params)
    pos = 0
    for size in chunk_sizes:
        inc.add_audio("main", pos, samples[pos:pos + size])
        pos += size
    assert pos == len(samples)
    return inc


class TestWholeVsChunked(unittest.TestCase):
    def test_reference_bounds(self):
        ref = segment_tracks(fixtures.fixture_two_bursts(), **dict(fixtures.PARAMS))
        self.assertEqual(bounds(ref), [(720, 1680), (3920, 4880)])

    def test_odd_chunk_sizes_match_reference(self):
        samples = fixtures.two_burst_samples()
        ref = segment_tracks(fixtures.fixture_two_bursts(), **dict(fixtures.PARAMS))
        for chunk_sizes in ([5600], [1000, 333, 80, 777, 3410],
                            [280] * 20, [1] * 160 + [5440]):
            inc = feed_in_chunks(samples, chunk_sizes, **dict(fixtures.PARAMS))
            self.assertEqual(bounds(inc.segments), bounds(ref),
                             "chunk sizes %r" % (chunk_sizes,))

    def test_chunked_delivery_with_gaps_matches_reference(self):
        # Deliver the gap-bridge fixture, then fill the gap late; the result
        # must equal the batch run over the final audio.
        inc = IncrementalSegmenter(fixtures.fixture_gap_bridge(),
                                   **dict(fixtures.PARAMS))
        inc.finalize()
        inc.add_audio("main", 1600, fixtures.tone(440, 2400))
        full = (fixtures.silence(800) + fixtures.tone(440, 800)
                + fixtures.tone(440, 2400)
                + fixtures.tone(550, 800) + fixtures.silence(800))
        ref_track = Track("main", fixtures.RATE)
        ref_track.add_samples(0, full)
        ref = segment_tracks([ref_track], **dict(fixtures.PARAMS))
        self.assertEqual(bounds(inc.segments), bounds(ref))


class TestGapVsTrueSilence(unittest.TestCase):
    def test_detection_treats_both_as_not_loud(self):
        segs = segment_tracks(fixtures.fixture_gap_vs_silence(),
                              **dict(fixtures.PARAMS))
        self.assertEqual(bounds(segs), [(720, 1680)])

    def test_export_distinguishes_gap_from_silence(self):
        inc = IncrementalSegmenter(fixtures.fixture_gap_vs_silence(),
                                   **dict(fixtures.PARAMS))
        inc.finalize()
        (exp,) = inc.export()
        data = exp.tracks["g"]
        # [720,800): true silence -> real 0.0 samples
        self.assertTrue(all(v == 0.0 for v in data[:80]))
        # [800,1600): the tone -> real non-zero samples
        self.assertTrue(any(v not in (None, 0.0) for v in data[80:880]))
        # [1600,1680): inside the gap -> None, not 0.0
        self.assertTrue(all(v is None for v in data[880:]))

    def test_gap_fill_removes_nones_from_export(self):
        inc = IncrementalSegmenter(fixtures.fixture_gap_bridge(),
                                   **dict(fixtures.PARAMS))
        inc.finalize()
        inc.add_audio("main", 1600, fixtures.tone(440, 2400))
        (exp,) = inc.export()
        self.assertTrue(all(v is not None for v in exp.tracks["main"]))


class TestCheckpointEquivalence(unittest.TestCase):
    def test_checkpoint_resume_equals_full_reference(self):
        samples = fixtures.two_burst_samples()
        ref = segment_tracks(fixtures.fixture_two_bursts(), **dict(fixtures.PARAMS))
        # Checkpoint at several moments, including inside a candidate silence
        # (1680: one silence window after burst 1, min_silence not yet met).
        for cut in (800, 1680, 1760, 4000):
            inc = IncrementalSegmenter([Track("main", fixtures.RATE)],
                                       **dict(fixtures.PARAMS))
            inc.add_audio("main", 0, samples[:cut])
            state = json.loads(json.dumps(inc.checkpoint()))
            track = Track("main", fixtures.RATE)
            track.add_samples(0, samples[:cut])
            restored = IncrementalSegmenter.restore([track], state)
            restored.add_audio("main", cut, samples[cut:])
            self.assertEqual(bounds(restored.segments), bounds(ref),
                             "checkpoint at %d" % cut)


class TestLatencyDeclaration(unittest.TestCase):
    def test_latency_formula(self):
        inc = IncrementalSegmenter(fixtures.fixture_two_bursts(),
                                   **dict(fixtures.PARAMS))
        # window + min_silence (rounded up to windows) + post_pad
        self.assertEqual(inc.latency_samples, 80 + 160 + 80)

    def test_latency_scales_with_parameters(self):
        inc = IncrementalSegmenter(
            fixtures.fixture_two_bursts(),
            **dict(fixtures.PARAMS, min_silence=200, post_pad=160))
        # min_silence 200 rounds up to 3 windows = 240
        self.assertEqual(inc.latency_samples, 80 + 240 + 160)


if __name__ == "__main__":
    unittest.main()
