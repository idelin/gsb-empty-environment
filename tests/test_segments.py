"""Segment extraction rules: min durations, padding, export integrity."""

import unittest

import fixtures
from silseg import IncrementalSegmenter, extract_segments, segment_tracks

W = fixtures.WINDOW


def bounds(segments):
    return [(s.start, s.end) for s in segments]


class TestExtractSegments(unittest.TestCase):
    def test_min_silence_merges_short_gaps(self):
        loud = [False] * 5 + [True] * 3 + [False] + [True] * 3 + [False] * 5
        # 1-window gap < min_silence (2 windows) -> bridged into one segment
        self.assertEqual(
            extract_segments(loud, W, 17 * W, min_silence=160),
            [(5 * W, 12 * W)],
        )
        # min_silence of 1 window keeps the gap: two segments
        self.assertEqual(
            extract_segments(loud, W, 17 * W, min_silence=80),
            [(5 * W, 8 * W), (9 * W, 12 * W)],
        )

    def test_min_sound_drops_short_noise(self):
        loud = [False] * 5 + [True] + [False] * 5
        self.assertEqual(extract_segments(loud, W, 11 * W, min_sound=160), [])
        self.assertEqual(
            extract_segments(loud, W, 11 * W, min_sound=0),
            [(5 * W, 6 * W)],
        )

    def test_padding_merges_and_clips(self):
        loud = [True] * 2 + [False] * 2 + [True] * 2
        segs = extract_segments(loud, W, 6 * W, pre_pad=160, post_pad=160)
        self.assertEqual(segs, [(0, 6 * W)])  # pads overlap and clip to total


class TestShortNoiseFixture(unittest.TestCase):
    def test_blip_absorbed_with_strict_min_sound(self):
        segs = segment_tracks(fixtures.fixture_short_noise(),
                              **dict(fixtures.PARAMS, min_sound=160))
        self.assertEqual(bounds(segs), [(720, 1680), (3920, 4880)])

    def test_blip_splits_with_relaxed_min_sound(self):
        segs = segment_tracks(fixtures.fixture_short_noise(),
                              **dict(fixtures.PARAMS, min_sound=0))
        self.assertEqual(bounds(segs),
                         [(720, 1680), (2560, 2800), (3920, 4880)])


class TestPaddingBridgeFixture(unittest.TestCase):
    def test_large_pads_merge(self):
        segs = segment_tracks(fixtures.fixture_padding_bridge(),
                              **dict(fixtures.PARAMS, pre_pad=240, post_pad=240))
        self.assertEqual(bounds(segs), [(560, 2240)])

    def test_small_pads_keep_two_segments(self):
        segs = segment_tracks(fixtures.fixture_padding_bridge(),
                              **dict(fixtures.PARAMS))
        self.assertEqual(bounds(segs), [(720, 1280), (1520, 2080)])


class TestExportIntegrity(unittest.TestCase):
    def test_overlapping_pads_exported_once(self):
        inc = IncrementalSegmenter(
            fixtures.fixture_padding_bridge(),
            **dict(fixtures.PARAMS, pre_pad=240, post_pad=240))
        inc.finalize()
        exported = inc.export()
        self.assertEqual(len(exported), 1)  # merged: one export, not two
        self.assertEqual(len(exported[0].tracks["main"]), 2240 - 560)

    def test_exports_are_disjoint_and_complete(self):
        inc = IncrementalSegmenter(fixtures.fixture_two_bursts(),
                                   **dict(fixtures.PARAMS))
        inc.finalize()
        exported = inc.export()
        spans = [(e.start, e.end) for e in exported]
        for i in range(len(spans) - 1):
            self.assertLessEqual(spans[i][1], spans[i + 1][0])
        for e in exported:
            self.assertEqual(len(e.tracks["main"]), e.duration)
            self.assertTrue(all(v is not None for v in e.tracks["main"]))


if __name__ == "__main__":
    unittest.main()
