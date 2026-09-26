"""Tests for stable per-sample streams under chunking and shuffling."""

import random
import unittest

from keyrng import key, map_samples, map_samples_chunked, normal, sample_key


def draw_noise(k, sample_id):
    """Per-sample random transform: 8 standard normals from the sample's key."""
    return normal(k, (8,))


class TestStableSampleStreams(unittest.TestCase):
    def setUp(self):
        self.base = key(4242)
        self.ids = [f"sample-{i:03d}" for i in range(20)]

    def test_chunking_does_not_change_results(self):
        reference = map_samples(self.base, self.ids, draw_noise)
        for chunk_size in (1, 3, 7, 20, 100):
            chunked = map_samples_chunked(self.base, self.ids, draw_noise, chunk_size)
            self.assertEqual(set(chunked), set(self.ids))
            for sid in self.ids:
                self.assertTrue(
                    reference[sid].exact_equal(chunked[sid]),
                    f"chunk_size={chunk_size} changed sample {sid}",
                )

    def test_shuffle_does_not_change_per_sample_values(self):
        reference = map_samples(self.base, self.ids, draw_noise)
        shuffled = list(self.ids)
        random.Random(0).shuffle(shuffled)
        after_shuffle = map_samples(self.base, shuffled, draw_noise)
        for sid in self.ids:
            self.assertTrue(reference[sid].exact_equal(after_shuffle[sid]))

    def test_stream_depends_on_id_not_position(self):
        # The same id appearing in a different batch composition keeps
        # exactly the same random values.
        solo = map_samples(self.base, ["sample-007"], draw_noise)["sample-007"]
        in_batch = map_samples(self.base, self.ids, draw_noise)["sample-007"]
        self.assertTrue(solo.exact_equal(in_batch))

    def test_distinct_ids_get_distinct_streams(self):
        keys = {sample_key(self.base, sid).data for sid in self.ids}
        self.assertEqual(len(keys), len(self.ids))

    def test_integer_ids_supported(self):
        result = map_samples_chunked(self.base, [0, 1, 2, 3], draw_noise, 2)
        self.assertEqual(set(result), {0, 1, 2, 3})

    def test_invalid_chunk_size(self):
        with self.assertRaises(ValueError):
            map_samples_chunked(self.base, self.ids, draw_noise, 0)


if __name__ == "__main__":
    unittest.main()
