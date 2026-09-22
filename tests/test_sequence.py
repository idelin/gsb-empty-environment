import unittest

from rdl.sequence import SequenceTracker, fwd_distance


class SequenceMathTests(unittest.TestCase):
    def test_fwd_distance(self):
        self.assertEqual(fwd_distance(3, 1, 16), 2)
        self.assertEqual(fwd_distance(1, 3, 16), 14)
        self.assertEqual(fwd_distance(0, 7, 8), 1)

    def test_headline_arrival_order(self):
        st = SequenceTracker(modulus=16, initial_seq=1, initial_abs=0)
        self.assertEqual(st.resolve(1).abs_index, 0)
        self.assertEqual(st.resolve(4).abs_index, 3)
        self.assertEqual(st.resolve(3).abs_index, 2)

    def test_wraparound_mapping(self):
        st = SequenceTracker(modulus=8, initial_seq=6, initial_abs=5,
                             reorder_window=3)
        expected = {6: 5, 7: 6, 0: 7, 1: 8}
        for seq, abs_index in expected.items():
            pos = st.resolve(seq)
            self.assertEqual(pos.abs_index, abs_index, seq)
            self.assertTrue(st.within_window(pos), seq)
        # distance 4 is outside the unambiguous window (mod/2 - 1 == 3)
        far = st.resolve(2)
        self.assertEqual(far.abs_index, 9)
        self.assertFalse(st.within_window(far))

    def test_seq_of_abs_wraps(self):
        st = SequenceTracker(modulus=8, initial_seq=6, initial_abs=5)
        self.assertEqual(st.seq_of_abs(7), 0)
        self.assertEqual(st.seq_of_abs(12), 5)
        self.assertEqual(st.seq_of_abs(13), 6)


if __name__ == "__main__":
    unittest.main()
