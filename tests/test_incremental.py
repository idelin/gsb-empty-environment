"""Incremental re-segmentation: patch events, stable IDs, checkpoints."""

import json
import unittest

import fixtures
from silseg import IncrementalSegmenter, Track


def bounds(segments):
    return [(s.start, s.end) for s in segments]


def make(samples=None):
    track = Track("main", fixtures.RATE)
    if samples is not None:
        track.add_samples(0, samples)
    return IncrementalSegmenter([track], **dict(fixtures.PARAMS))


class TestAppendEvents(unittest.TestCase):
    def test_initial_finalize_emits_adds(self):
        inc = make(fixtures.two_burst_samples())
        events = inc.finalize()
        self.assertEqual([e["op"] for e in events], ["add", "add"])
        self.assertEqual(bounds(inc.segments), [(720, 1680), (3920, 4880)])
        self.assertEqual([s.seg_id for s in inc.segments], ["S0001", "S0002"])

    def test_silent_append_keeps_ids_and_emits_nothing(self):
        inc = make(fixtures.two_burst_samples())
        inc.finalize()
        events = inc.add_audio("main", 5600, fixtures.silence(800))
        self.assertEqual(events, [])
        self.assertEqual([s.seg_id for s in inc.segments], ["S0001", "S0002"])

    def test_new_burst_appends_one_add_event(self):
        inc = make(fixtures.two_burst_samples())
        inc.finalize()
        events = inc.add_audio("main", 5600,
                               fixtures.silence(720) + fixtures.tone(440, 800))
        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]["op"], "add")
        self.assertEqual(events[0]["id"], "S0003")
        # tone spans windows 79-88; end clips to the new extent (7120)
        self.assertEqual(bounds(inc.segments),
                         [(720, 1680), (3920, 4880), (6240, 7120)])


class TestMergeAndSplit(unittest.TestCase):
    def test_late_audio_merges_then_retraction_splits(self):
        inc = IncrementalSegmenter(fixtures.fixture_gap_bridge(),
                                   **dict(fixtures.PARAMS))
        inc.finalize()
        self.assertEqual(bounds(inc.segments), [(720, 1680), (3920, 4880)])

        # Late audio lands in the gap between the two segments -> merge.
        events = inc.add_audio("main", 1600, fixtures.tone(440, 2400))
        self.assertEqual(bounds(inc.segments), [(720, 4880)])
        revise = [e for e in events if e["op"] == "revise"]
        remove = [e for e in events if e["op"] == "remove"]
        self.assertEqual(len(revise), 1)
        self.assertEqual(revise[0]["id"], "S0001")
        self.assertEqual(revise[0]["new"], [720, 4880])
        self.assertEqual(len(remove), 1)
        self.assertEqual(remove[0],
                         {"op": "remove", "id": "S0002",
                          "reason": "merged", "merged_into": "S0001"})

        # Retracting the fill (marked as silence) -> re-split.
        events = inc.revise_silence(1600, 4000, is_silence=True)
        self.assertEqual(bounds(inc.segments), [(720, 1680), (3920, 4880)])
        ops = [e["op"] for e in events]
        self.assertEqual(ops.count("revise"), 1)
        self.assertEqual(ops.count("add"), 1)
        # The keeper ID survives the split; the other half gets a fresh ID.
        self.assertEqual(inc.segments[0].seg_id, "S0001")
        self.assertNotEqual(inc.segments[1].seg_id, "S0001")

    def test_merge_result_matches_batch_reference(self):
        inc = IncrementalSegmenter(fixtures.fixture_gap_bridge(),
                                   **dict(fixtures.PARAMS))
        inc.finalize()
        inc.add_audio("main", 1600, fixtures.tone(440, 2400))
        # Batch reference: the same audio present from the start.
        ref_track = Track("main", fixtures.RATE)
        ref_track.add_samples(0, fixtures.silence(800) + fixtures.tone(440, 800)
                              + fixtures.tone(440, 2400)
                              + fixtures.tone(550, 800) + fixtures.silence(800))
        from silseg import segment_tracks
        ref = segment_tracks([ref_track], **dict(fixtures.PARAMS))
        self.assertEqual(bounds(inc.segments), bounds(ref))


class TestReviseExportedSegment(unittest.TestCase):
    def test_exported_segment_corrected_by_revise_event(self):
        samples = (fixtures.silence(800) + fixtures.tone(440, 800)
                   + fixtures.silence(2400) + fixtures.tone(550, 800))
        inc = make(samples)  # extent 4800
        inc.finalize()
        exported = {e.seg_id: e for e in inc.export()}
        self.assertEqual(exported["S0002"].end, 4800)
        # Extra audio extends the second burst; the exported segment is
        # corrected through a revise event, keeping its ID.
        events = inc.add_audio("main", 4800, fixtures.tone(550, 400))
        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]["op"], "revise")
        self.assertEqual(events[0]["id"], "S0002")
        self.assertEqual(events[0]["old"], [3920, 4800])
        self.assertEqual(events[0]["new"], [3920, 5200])
        self.assertEqual(inc.segments[1].seg_id, "S0002")


class TestSilenceMarkers(unittest.TestCase):
    def test_marker_on_existing_silence_is_a_noop(self):
        inc = make(fixtures.two_burst_samples())
        inc.finalize()
        events = inc.revise_silence(2000, 2160, is_silence=True)
        self.assertEqual(events, [])
        self.assertEqual(bounds(inc.segments), [(720, 1680), (3920, 4880)])

    def test_marker_splits_a_segment(self):
        inc = make(fixtures.two_burst_samples())
        inc.finalize()
        # Mark 3 windows (1040..1280) inside burst 1 as silence.
        events = inc.revise_silence(1040, 1280, is_silence=True)
        self.assertEqual(bounds(inc.segments), [(720, 1120), (1200, 1680),
                                                (3920, 4880)])
        ops = [e["op"] for e in events]
        self.assertIn("revise", ops)
        self.assertIn("add", ops)
        # Undoing the marker restores the original segment and ID stability.
        events = inc.revise_silence(1040, 1280, is_silence=False)
        self.assertEqual(bounds(inc.segments), [(720, 1680), (3920, 4880)])


class TestCheckpoint(unittest.TestCase):
    def test_checkpoint_is_json_serializable(self):
        inc = make(fixtures.two_burst_samples())
        inc.finalize()
        json.dumps(inc.checkpoint())

    def test_restore_during_candidate_silence(self):
        samples = fixtures.two_burst_samples()
        # Stop at 1680: burst 1 ended at 1600, only 1 silence window seen,
        # min_silence (2 windows) not yet reached -> candidate silence.
        inc = make()
        inc.add_audio("main", 0, samples[:1680])
        state = json.loads(json.dumps(inc.checkpoint()))

        restored_track = Track("main", fixtures.RATE)
        restored_track.add_samples(0, samples[:1680])
        restored = IncrementalSegmenter.restore([restored_track], state)

        inc.add_audio("main", 1680, samples[1680:])
        restored.add_audio("main", 1680, samples[1680:])
        self.assertEqual(bounds(restored.segments), bounds(inc.segments))
        self.assertEqual([s.seg_id for s in restored.segments],
                         [s.seg_id for s in inc.segments])

    def test_restore_then_gap_fill(self):
        # Rewind history must survive checkpointing: fill a gap after restore.
        inc = IncrementalSegmenter(fixtures.fixture_gap_bridge(),
                                   **dict(fixtures.PARAMS))
        inc.finalize()
        state = json.loads(json.dumps(inc.checkpoint()))
        restored = IncrementalSegmenter.restore(fixtures.fixture_gap_bridge(),
                                                state)
        restored.add_audio("main", 1600, fixtures.tone(440, 2400))
        self.assertEqual(bounds(restored.segments), [(720, 4880)])


if __name__ == "__main__":
    unittest.main()
