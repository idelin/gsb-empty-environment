import unittest

from hmm import HMM, ModelValidationError, PiecewiseHMM


def base_kwargs():
    return dict(
        initial=[0.6, 0.4],
        transition=[[0.7, 0.3], [0.4, 0.6]],
        emission=[{"a": 0.5, "b": 0.5}, {"a": 0.1, "b": 0.9}],
    )


class TestModelValidation(unittest.TestCase):
    def test_valid_model_constructs(self):
        model = HMM(**base_kwargs())
        self.assertEqual(model.n_states, 2)
        self.assertEqual(model.n_segments, 1)

    def test_initial_must_sum_to_one(self):
        kw = base_kwargs()
        kw["initial"] = [0.6, 0.5]
        with self.assertRaises(ModelValidationError):
            HMM(**kw)

    def test_zero_row_is_rejected_not_renormalised(self):
        kw = base_kwargs()
        kw["transition"] = [[0.0, 0.0], [0.4, 0.6]]
        with self.assertRaises(ModelValidationError):
            HMM(**kw)

    def test_negative_probability_rejected(self):
        kw = base_kwargs()
        kw["transition"] = [[1.2, -0.2], [0.4, 0.6]]
        with self.assertRaises(ModelValidationError):
            HMM(**kw)

    def test_non_square_transition_rejected(self):
        kw = base_kwargs()
        kw["transition"] = [[0.7, 0.3]]
        with self.assertRaises(ModelValidationError):
            HMM(**kw)

    def test_emission_row_must_sum_to_one(self):
        kw = base_kwargs()
        kw["emission"] = [{"a": 0.5, "b": 0.6}, {"a": 0.1, "b": 0.9}]
        with self.assertRaises(ModelValidationError):
            HMM(**kw)

    def test_emission_size_mismatch_rejected(self):
        kw = base_kwargs()
        kw["emission"] = [{"a": 1.0}]
        with self.assertRaises(ModelValidationError):
            HMM(**kw)

    def test_list_emission_rows_become_dicts(self):
        model = HMM([1.0, 0.0], [[0.5, 0.5], [0.5, 0.5]], [[0.2, 0.8], [0.9, 0.1]])
        self.assertAlmostEqual(model.emission_prob(0, 1), 0.8)

    def test_unknown_observation_has_zero_probability(self):
        model = HMM(**base_kwargs())
        self.assertEqual(model.emission_prob(0, "zzz"), 0.0)

    def test_missing_observation_is_uninformative(self):
        model = HMM(**base_kwargs())
        self.assertEqual(model.emission_prob(0, None), 1.0)
        self.assertEqual(model.emission_prob(1, None), 1.0)


class TestPiecewiseValidation(unittest.TestCase):
    def test_boundary_count_must_match(self):
        with self.assertRaises(ModelValidationError):
            PiecewiseHMM(
                [1.0, 0.0],
                [[[0.5, 0.5], [0.5, 0.5]], [[0.9, 0.1], [0.1, 0.9]]],
                [],
                [{"a": 1.0}, {"a": 1.0}],
            )

    def test_boundaries_must_increase(self):
        with self.assertRaises(ModelValidationError):
            PiecewiseHMM(
                [1.0, 0.0],
                [[[0.5, 0.5]] * 2] * 3,
                [3, 3],
                [{"a": 1.0}, {"a": 1.0}],
            )

    def test_segment_selection(self):
        model = PiecewiseHMM(
            [1.0, 0.0],
            [[[1.0, 0.0], [0.0, 1.0]], [[0.0, 1.0], [1.0, 0.0]]],
            [2],
            [{"a": 1.0}, {"a": 1.0}],
        )
        self.assertEqual(model.transition_matrix(0), [[1.0, 0.0], [0.0, 1.0]])
        self.assertEqual(model.transition_matrix(1), [[1.0, 0.0], [0.0, 1.0]])
        self.assertEqual(model.transition_matrix(2), [[0.0, 1.0], [1.0, 0.0]])
        self.assertEqual(model.n_segments, 2)


if __name__ == "__main__":
    unittest.main()
