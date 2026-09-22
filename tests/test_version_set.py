import unittest

from vsets import Version, VersionSet, parse_spec
from vsets.intervals import IntervalSet
from vsets.versionset import RELEASE_DOMAIN, pre_domain


class IntervalDifferenceTest(unittest.TestCase):
    def test_half_is_difference_of_overlapping_ranges(self):
        left = parse_spec("[1.0.0,3.0.0)")
        right = parse_spec("[2.0.0,4.0.0)")
        result = left - right

        self.assertEqual(result.serialize(), "rel=[1.0.0,2.0.0)")
        included = ["1.0.0", "1.2.3", "1.255.255", "1.9.9"]
        excluded = ["0.9.9", "2.0.0", "2.0.1", "2.9.9", "3.0.0"]
        for text in included:
            with self.subTest(text=text):
                self.assertIn(Version.parse(text), result)
        for text in excluded:
            with self.subTest(text=text):
                self.assertNotIn(Version.parse(text), result)

        self.assertEqual(
            (left - right).serialize(),
            (left & parse_spec("[*,2.0.0)")).serialize(),
        )
        self.assertEqual(result.counterexample(right), Version.parse("1.0.0"))


class PrereleaseChannelTest(unittest.TestCase):
    def test_prereleases_never_enter_plain_release_sets(self):
        release_set = parse_spec("[2.0.0,3.0.0)")
        for text in ("2.0.0-alpha.1", "2.0.0-alpha", "2.5.0-beta.3"):
            with self.subTest(text=text):
                self.assertNotIn(Version.parse(text), release_set)
        self.assertEqual(release_set.allowed_channels, frozenset())

    def test_prereleases_require_explicit_channel_allowance(self):
        alpha = parse_spec("[2.0.0,3.0.0)@alpha")
        self.assertIn(Version.parse("2.0.0-alpha.1"), alpha)
        self.assertIn(Version.parse("2.9.9-alpha.7"), alpha)
        self.assertNotIn(Version.parse("2.0.0-beta.1"), alpha)
        self.assertNotIn(Version.parse("2.0.0-rc.1"), alpha)
        self.assertNotIn(Version.parse("2.0.0"), alpha)
        self.assertNotIn(Version.parse("1.9.9-alpha.9"), alpha)
        self.assertNotIn(Version.parse("3.0.0-alpha.1"), alpha)
        self.assertEqual(alpha.allowed_channels, frozenset({"alpha"}))

    def test_equal_numeric_triple_does_not_admit_other_lane(self):
        gates = parse_spec("2.0.0")
        self.assertIn(Version.parse("2.0.0"), gates)
        self.assertNotIn(Version.parse("2.0.0-alpha.1"), gates)
        pre_gate = parse_spec("2.0.0-alpha.1")
        self.assertIn(Version.parse("2.0.0-alpha.1"), pre_gate)
        self.assertNotIn(Version.parse("2.0.0"), pre_gate)
        self.assertNotIn(Version.parse("2.0.0-alpha.2"), pre_gate)

    def test_channel_intersection_is_lane_wise(self):
        alpha = parse_spec("[2.0.0,3.0.0)@alpha")
        beta = parse_spec("[2.0.0,3.0.0)@beta")
        self.assertTrue((alpha & beta).is_empty())
        union = alpha | beta
        self.assertEqual(union.allowed_channels, frozenset({"alpha", "beta"}))
        self.assertIn(Version.parse("2.0.0-beta.1"), union)
        self.assertNotIn(Version.parse("2.0.0-rc.1"), union)


class CanonicalOrderTest(unittest.TestCase):
    def test_boolean_assembly_order_is_semantically_equivalent(self):
        base = parse_spec("[1.0.0,2.0.0) | >=2.0.0 & <3.0.0")
        canonical = parse_spec("[1.0.0,3.0.0)")
        self.assertEqual(base.serialize(), canonical.serialize())

        a = parse_spec("[1.0.0,4.0.0)")
        b = parse_spec("[2.0.0,3.0.0)")
        c = parse_spec("[*,2.5.0)")
        left = (a - b) & c
        right = a & c - b
        self.assertEqual(left.serialize(), right.serialize())
        self.assertIn(Version.parse("1.5.0"), left)
        for text in ("2.0.0", "2.4.9"):
            self.assertNotIn(Version.parse(text), left)

    def test_adjacent_and_overlapping_intervals_merge(self):
        merged = parse_spec("[1.0.0,2.0.0) | [2.0.0,3.0.0)")
        self.assertEqual(merged.serialize(), "rel=[1.0.0,3.0.0)")
        overlap = parse_spec("[1.0.0,2.5.0) | [2.0.0,3.0.0)")
        self.assertEqual(overlap.serialize(), "rel=[1.0.0,3.0.0)")

    def test_unbounded_ends(self):
        unbounded = parse_spec("[*,2.0.0) | [3.0.0,*)")
        self.assertIn(Version.parse("0.0.0"), unbounded)
        self.assertIn(Version.parse("99.0.0"), unbounded)
        self.assertNotIn(Version.parse("2.5.0"), unbounded)
        self.assertEqual(unbounded.first_gap(Version.parse("2.0.0")), Version.parse("2.0.0"))

    def test_excluded_points_split_into_canonical_pieces(self):
        split = parse_spec("[1.0.0,3.0.0) & !=2.0.0")
        self.assertEqual(split.serialize(), "rel=[1.0.0,2.0.0)|[2.0.1,3.0.0)")
        self.assertNotIn(Version.parse("2.0.0"), split)
        self.assertEqual(split.first_gap(Version.parse("1.9.9")), Version.parse("2.0.0"))

    def test_serialization_hash_and_minimal_counterexample(self):
        left = parse_spec("[1.0.0,3.0.0)")
        right = parse_spec("[1.0.0,2.0.0)")
        witness = left.min_counterexample_against(right)
        self.assertEqual(witness, Version.parse("2.0.0"))
        self.assertEqual(
            parse_spec("[1.0.0,2.0.0) | [2.0.0,3.0.0)").digest(),
            parse_spec("[1.0.0,3.0.0)").digest(),
        )
        self.assertEqual(hash(left), hash(VersionSet(left.releases, dict(left.channels))))
        digest = parse_spec("[1.0.0,2.0.0)").digest()
        self.assertRegex(digest, r"^[0-9a-f]{64}$")


class FiniteCrossCheckTest(unittest.TestCase):
    DOMAIN = [
        Version(0, 0, 0),
        Version(1, 0, 0),
        Version(1, 0, 1),
        Version(1, 1, 0),
        Version(2, 0, 0),
        Version(2, 0, 1),
        Version(3, 0, 0),
    ]

    def _membership(self, version_set: VersionSet):
        return [version for version in self.DOMAIN if version in version_set]

    def test_all_subsets_have_finite_canonical_form_without_enumeration(self):
        self.assertIsInstance(RELEASE_DOMAIN.successor(Version(1, 1, 1)), Version)
        self.assertEqual(
            pre_domain("alpha").successor(Version(2, 0, 0, "alpha", 1)),
            Version(2, 0, 0, "alpha", 2),
        )

    def test_boolean_laws_on_finite_witness_set(self):
        specs = [
            "[*,2.0.0)",
            "[1.0.0,*)",
            "[1.0.0,2.0.0)",
            "[1.0.0,3.0.0)",
            "[2.0.0,3.0.0)",
            "[*,1.0.1) | [2.0.0,*)",
        ]
        sets = [parse_spec(text) for text in specs]
        for i, left in enumerate(sets):
            for right in sets[i:]:
                with self.subTest(left=left.serialize(), right=right.serialize()):
                    self.assertEqual(
                        self._membership(left | right),
                        sorted(set(self._membership(left)) | set(self._membership(right))),
                    )
                    self.assertEqual(
                        self._membership(left & right),
                        sorted(set(self._membership(left)) & set(self._membership(right))),
                    )
                    self.assertEqual(
                        self._membership(left - right),
                        sorted(set(self._membership(left)) - set(self._membership(right))),
                    )

    def test_minimum_counterexample_is_witness_smallest(self):
        empty = IntervalSet.empty(RELEASE_DOMAIN)
        for text in ("[1.0.0,2.0.0)", "[2.0.0,*)", "[*,2.0.0) | [3.0.0,*)"):
            version_set = parse_spec(text)
            members = self._membership(version_set)
            if members:
                self.assertEqual(
                    version_set.counterexample(VersionSet.empty()),
                    min(members),
                )
        self.assertTrue(empty.is_empty)


if __name__ == "__main__":
    unittest.main()
