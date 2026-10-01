import unittest

from k_anonymizer.hierarchy import Hierarchy
from k_anonymizer.loss import combination_loss, field_loss


def two_leaf_hierarchy():
    return Hierarchy("g", ["F", "M"], {"F": ["*"], "M": ["*"]})


class TestLoss(unittest.TestCase):
    def test_field_loss_bounds(self):
        h = two_leaf_hierarchy()
        self.assertEqual(field_loss(h, "F"), 0.0)
        self.assertEqual(field_loss(h, "*"), 1.0)

    def test_field_loss_partial(self):
        h = Hierarchy(
            "a",
            ["0", "1", "2"],
            {"0": ["lo"], "1": ["lo"], "2": ["hi"], "lo": ["*"], "hi": ["*"]},
        )
        self.assertAlmostEqual(field_loss(h, "lo"), 0.5)
        self.assertAlmostEqual(field_loss(h, "hi"), 0.0)
        self.assertAlmostEqual(field_loss(h, "*"), 1.0)

    def test_combination_loss_range_and_value(self):
        h = two_leaf_hierarchy()
        records = [("F",), ("M",), ("F",)]
        self.assertEqual(combination_loss(records, [h], (0,)), 0.0)
        self.assertEqual(combination_loss(records, [h], (1,)), 1.0)
        self.assertEqual(combination_loss([], [h], (1,)), 0.0)


if __name__ == "__main__":
    unittest.main()
