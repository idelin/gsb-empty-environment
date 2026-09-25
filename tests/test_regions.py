import unittest

from rulecov.regions import (
    EnumSet,
    IntRange,
    intersect_constraints,
    intersect_regions,
    is_disjoint,
    subtract_constraint,
    subtract_region,
)


class TestConstraintOps(unittest.TestCase):
    def test_enum_intersection(self):
        a = EnumSet(["gold", "silver"])
        b = EnumSet(["silver", "bronze"])
        self.assertEqual(intersect_constraints(a, b), EnumSet(["silver"]))

    def test_enum_disjoint(self):
        self.assertIsNone(
            intersect_constraints(EnumSet(["gold"]), EnumSet(["bronze"])))

    def test_int_intersection(self):
        self.assertEqual(intersect_constraints(IntRange(0, 10), IntRange(5, 20)),
                         IntRange(5, 10))

    def test_int_touching_is_disjoint(self):
        self.assertIsNone(
            intersect_constraints(IntRange(0, 4), IntRange(5, 9)))

    def test_int_subtract_splits_into_two(self):
        pieces = subtract_constraint(IntRange(0, 10), IntRange(4, 6))
        self.assertEqual(pieces, [IntRange(0, 3), IntRange(7, 10)])

    def test_int_subtract_all(self):
        self.assertEqual(subtract_constraint(IntRange(0, 5), IntRange(0, 5)), [])

    def test_enum_subtract(self):
        pieces = subtract_constraint(EnumSet(["a", "b", "c"]), EnumSet(["b"]))
        self.assertEqual(pieces, [EnumSet(["a", "c"])])


class TestRegionOps(unittest.TestCase):
    def setUp(self):
        self.domain = {
            "tier": EnumSet(["gold", "silver", "bronze"]),
            "total": IntRange(0, 10000),
        }

    def test_disjoint_on_shared_fact(self):
        a = {"tier": EnumSet(["gold"])}
        b = {"tier": EnumSet(["silver"])}
        self.assertTrue(is_disjoint(a, b))

    def test_unconstrained_fact_is_not_disjoint(self):
        a = {"tier": EnumSet(["gold"])}
        b = {"total": IntRange(0, 5)}
        self.assertFalse(is_disjoint(a, b))

    def test_intersect_regions_merges(self):
        a = {"tier": EnumSet(["gold"])}
        b = {"total": IntRange(10, 20)}
        self.assertEqual(intersect_regions(a, b),
                         {"tier": EnumSet(["gold"]), "total": IntRange(10, 20)})

    def test_subtract_uses_domain_for_missing_fact(self):
        region = {"total": IntRange(0, 100)}
        box = {"tier": EnumSet(["gold"])}
        fragments = subtract_region(region, box, self.domain)
        self.assertEqual(
            fragments,
            [{"total": IntRange(0, 100),
              "tier": EnumSet(["silver", "bronze"])}])

    def test_subtract_two_dimensions(self):
        region = {"tier": EnumSet(["gold", "silver"]),
                  "total": IntRange(0, 100)}
        box = {"tier": EnumSet(["gold"]), "total": IntRange(50, 150)}
        fragments = subtract_region(region, box, self.domain)
        # silver rows survive untouched; gold rows survive below 50.
        self.assertEqual(
            fragments,
            [{"tier": EnumSet(["silver"]), "total": IntRange(0, 100)},
             {"tier": EnumSet(["gold"]), "total": IntRange(0, 49)}])


if __name__ == "__main__":
    unittest.main()
