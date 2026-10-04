import unittest

from hmm import HMM, MISSING, HMMValidationError


def make_model():
    return HMM(
        n_states=2,
        initial=(0.6, 0.4),
        transition=((0.7, 0.3), (0.4, 0.6)),
        emission=({"a": 0.5, "b": 0.5}, {"a": 0.1, "b": 0.9}),
    )


class TestValidation(unittest.TestCase):
    def test_valid_model_constructs(self):
        m = make_model()
        self.assertEqual(m.n_states, 2)
        self.assertEqual(m.initial, (0.6, 0.4))

    def test_negative_probability_rejected(self):
        with self.assertRaises(HMMValidationError):
            HMM(2, (1.2, -0.2), ((0.5, 0.5), (0.5, 0.5)),
                ({"a": 1.0}, {"a": 1.0}))

    def test_initial_not_summing_to_one_rejected(self):
        with self.assertRaises(HMMValidationError):
            HMM(2, (0.5, 0.4), ((0.5, 0.5), (0.5, 0.5)),
                ({"a": 1.0}, {"a": 1.0}))

    def test_transition_row_not_summing_to_one_rejected(self):
        with self.assertRaises(HMMValidationError):
            HMM(2, (0.5, 0.5), ((0.5, 0.6), (0.5, 0.5)),
                ({"a": 1.0}, {"a": 1.0}))

    def test_transition_wrong_shape_rejected(self):
        with self.assertRaises(HMMValidationError):
            HMM(2, (0.5, 0.5), ((0.5, 0.5),), ({"a": 1.0}, {"a": 1.0}))
        with self.assertRaises(HMMValidationError):
            HMM(2, (0.5, 0.5), ((0.5, 0.5), (1.0,)), ({"a": 1.0}, {"a": 1.0}))

    def test_emission_not_summing_to_one_rejected(self):
        with self.assertRaises(HMMValidationError):
            HMM(1, (1.0,), ((1.0,),), ({"a": 0.5, "b": 0.4},))

    def test_emission_none_symbol_reserved(self):
        with self.assertRaises(HMMValidationError):
            HMM(1, (1.0,), ((1.0,),), ({None: 1.0},))

    def test_zero_probabilities_allowed(self):
        m = HMM(2, (1.0, 0.0), ((1.0, 0.0), (0.0, 1.0)),
                ({"a": 1.0}, {"b": 1.0}))
        self.assertEqual(m.emission_prob(0, "b"), 0.0)

    def test_emission_prob_semantics(self):
        m = make_model()
        self.assertEqual(m.emission_prob(0, MISSING), 1.0)
        self.assertEqual(m.emission_prob(1, MISSING), 1.0)
        self.assertEqual(m.emission_prob(0, "nonexistent"), 0.0)
        self.assertAlmostEqual(m.emission_prob(1, "b"), 0.9)


if __name__ == "__main__":
    unittest.main()
