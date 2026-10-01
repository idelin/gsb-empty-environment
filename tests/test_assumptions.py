import unittest

from amplification.assumptions import (
    AssumptionError,
    SamplingAssumption,
    check_composable,
)


class TestAssumptions(unittest.TestCase):
    def test_default_assumption_is_applicable(self):
        SamplingAssumption().check_formula_applicable()

    def test_rejects_unsupported_models(self):
        with self.assertRaises(AssumptionError):
            SamplingAssumption(scheme="without_replacement").check_formula_applicable()
        with self.assertRaises(AssumptionError):
            SamplingAssumption(adjacency="replace_one").check_formula_applicable()
        with self.assertRaises(AssumptionError):
            SamplingAssumption(base_delta=1e-6).check_formula_applicable()
        with self.assertRaises(AssumptionError):
            SamplingAssumption(dataset_size=0).check_formula_applicable()

    def test_composable_requires_identical_models(self):
        a = SamplingAssumption(dataset_size=500)
        check_composable([a, a])
        with self.assertRaises(AssumptionError):
            check_composable([a, SamplingAssumption(dataset_size=600)])
        with self.assertRaises(AssumptionError):
            check_composable([])


if __name__ == "__main__":
    unittest.main()
