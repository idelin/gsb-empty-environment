"""Tests for symbolic version sets: algebra, channels, targets, canonical
form, minimal counterexamples, serialization and hashing."""

import unittest

from versionset import LabelSet, Version, VersionSet

STABLE_B = LabelSet.of(["stable", "b"])


class DifferenceTest(unittest.TestCase):
    def test_difference_first_half(self):
        a = VersionSet.parse("[1.0.0,3.0.0)")
        b = VersionSet.parse("[2.0.0,4.0.0)")
        diff = a.difference(b)
        self.assertEqual(diff, VersionSet.parse("[1.0.0,2.0.0)"))
        self.assertTrue(diff.contains("1.0.0"))
        self.assertTrue(diff.contains("1.9.9"))
        self.assertFalse(diff.contains("2.0.0"))
        self.assertFalse(diff.contains("2.5.0"))
        self.assertFalse(diff.contains("0.9.0"))
        # Canonical form is exactly the single first-half interval.
        self.assertEqual(
            diff.canonical(),
            '{"components": [{"channels": {"only": ["stable"]}, '
            '"intervals": ["[1.0.0, 2.0.0)"], "targets": {"all": true}}]}')

    def test_excluded_points(self):
        s = VersionSet.parse("[1.0.0,2.0.0)").difference(
            VersionSet.point(Version.parse("1.3.0")))
        self.assertTrue(s.contains("1.2.0"))
        self.assertFalse(s.contains("1.3.0"))
        self.assertEqual(s.min_version(), Version.parse("1.0.0"))
        # Excluding the lower bound: the smallest stable version strictly
        # above 1.0.0 is 1.0.1 (1.0.1a0 is a prerelease, not allowed).
        s2 = VersionSet.parse("[1.0.0,2.0.0)").difference(
            VersionSet.point(Version.parse("1.0.0")))
        self.assertEqual(s2.min_version(), Version.parse("1.0.1"))


class PrereleaseChannelTest(unittest.TestCase):
    def test_prerelease_only_when_channel_allowed(self):
        rng = VersionSet.parse("[1.0.0,2.0.0)")
        self.assertFalse(rng.contains("1.5.0b1"))   # default: stable only
        self.assertTrue(rng.contains("1.5.0"))
        rng_b = VersionSet.parse("[1.0.0,2.0.0)", channels=STABLE_B)
        self.assertTrue(rng_b.contains("1.5.0b1"))
        self.assertFalse(rng_b.contains("1.5.0rc1"))  # rc not allowed
        self.assertTrue(rng_b.contains("2.0.0b1"))  # 2.0.0b1 < 2.0.0: inside
        self.assertFalse(rng_b.contains("2.0.0"))    # open upper bound
        self.assertFalse(rng_b.contains("2.0.1b1"))  # above the upper bound

    def test_same_numeric_parts_do_not_leak(self):
        # A prerelease is never equal to, or contained as, its stable triple.
        point = VersionSet.point(Version.parse("1.5.0"))
        self.assertFalse(point.contains("1.5.0b1"))
        degenerate = VersionSet.parse("[1.5.0,1.5.0]")
        self.assertFalse(degenerate.contains("1.5.0b1"))
        self.assertNotEqual(Version.parse("1.5.0"), Version.parse("1.5.0b1"))
        # And a prerelease point does not admit the stable either.
        pre = VersionSet.point(Version.parse("1.5.0b1"))
        self.assertFalse(pre.contains("1.5.0"))

    def test_prerelease_min_version_uses_channel(self):
        # Smallest b-channel version >= 1.0.0: prereleases of 1.0.0 sort
        # before 1.0.0 itself, so the answer is the next triple's b0.
        s = VersionSet.parse(">=1.0.0", channels=LabelSet.of(["b"]))
        self.assertEqual(s.min_version(), Version.parse("1.0.1b0"))


class NormalizationOrderTest(unittest.TestCase):
    """Normalization and boolean operation order are semantically
    equivalent: regrouping the same operations never changes the set."""

    def setUp(self):
        self.a = VersionSet.parse("[1.0.0,2.5.0)", channels=STABLE_B)
        self.b = VersionSet.parse("[2.0.0,3.0.0]")
        self.c = VersionSet.parse("(1.5.0,2.2.0]", channels=STABLE_B)

    def test_distributive_laws(self):
        a, b, c = self.a, self.b, self.c
        self.assertEqual(a.union(b).intersect(c),
                         a.intersect(c).union(b.intersect(c)))
        self.assertEqual(a.intersect(b.union(c)),
                         a.intersect(b).union(a.intersect(c)))

    def test_associativity_commutativity(self):
        a, b, c = self.a, self.b, self.c
        self.assertEqual(a.union(b), b.union(a))
        self.assertEqual(a.intersect(b), b.intersect(a))
        self.assertEqual(a.union(b).union(c), a.union(b.union(c)))
        self.assertEqual(a.intersect(b).intersect(c),
                         a.intersect(b.intersect(c)))

    def test_difference_laws(self):
        a, b, c = self.a, self.b, self.c
        self.assertEqual(a.difference(b.union(c)),
                         a.difference(b).intersect(a.difference(c)))
        self.assertEqual(a.difference(b).difference(c),
                         a.difference(b.union(c)))

    def test_renormalization_is_idempotent(self):
        a, b, c = self.a, self.b, self.c
        s = a.union(b).difference(c)
        self.assertEqual(VersionSet(s.components).canonical(), s.canonical())
        # Unioning a subset back does not change the set.
        self.assertEqual(s.union(s.intersect(a)), s)


class BoundaryTest(unittest.TestCase):
    def test_unbounded_endpoints(self):
        s = VersionSet.parse(">=1.0.0")
        self.assertTrue(s.contains("999.0.0"))
        self.assertFalse(s.contains("0.9.9"))
        self.assertEqual(s.min_version(), Version.parse("1.0.0"))
        low = VersionSet.parse("<2.0.0")
        # Stable-only channels: the minimum is 0.0.0, not 0.0.0a0.
        self.assertEqual(low.min_version(), Version.parse("0.0.0"))
        self.assertEqual(s.intersect(low),
                         VersionSet.parse("[1.0.0,2.0.0)"))

    def test_adjacent_open_boundaries_keep_the_hole(self):
        s = VersionSet.parse("[1.0.0,2.0.0)").union(
            VersionSet.parse("(2.0.0,3.0.0]"))
        self.assertTrue(s.contains("1.5.0"))
        self.assertTrue(s.contains("2.5.0"))
        self.assertTrue(s.contains("3.0.0"))
        self.assertFalse(s.contains("2.0.0"))  # the hole stays a hole
        closed = VersionSet.parse("[1.0.0,3.0.0]")
        self.assertEqual(s.counterexample(closed), Version.parse("2.0.0"))
        # Touching intervals with one closed side do merge.
        merged = VersionSet.parse("[1.0.0,2.0.0)").union(
            VersionSet.parse("[2.0.0,3.0.0]"))
        self.assertEqual(merged, closed)


class CounterexampleTest(unittest.TestCase):
    def test_minimal_counterexample(self):
        a = VersionSet.parse("[1.0.0,2.0.0)")
        self.assertIsNone(a.counterexample(VersionSet.parse("[1.0.0,2.0.0)")))
        self.assertEqual(a.counterexample(VersionSet.parse("[1.0.0,2.0.0]")),
                         Version.parse("2.0.0"))
        self.assertEqual(a.counterexample(VersionSet.parse("[0.5.0,2.0.0)")),
                         Version.parse("0.5.0"))

    def test_counterexample_respects_channels(self):
        stable_only = VersionSet.parse("[1.0.0,2.0.0)")
        with_b = VersionSet.parse("[1.0.0,2.0.0)", channels=STABLE_B)
        self.assertEqual(stable_only.counterexample(with_b),
                         Version.parse("1.0.1b0"))


class TargetTest(unittest.TestCase):
    def test_target_conditions_gate_membership(self):
        s = VersionSet.parse("[1.0.0,2.0.0)", targets=["linux"])
        self.assertTrue(s.contains("1.5.0", target="linux"))
        self.assertFalse(s.contains("1.5.0", target="macos"))
        self.assertFalse(s.contains("1.5.0"))  # no target context: no match
        any_target = VersionSet.parse("[1.0.0,2.0.0)")
        self.assertTrue(any_target.contains("1.5.0"))

    def test_target_algebra(self):
        linux = VersionSet.parse("[1.0.0,2.0.0)", targets=["linux"])
        macos = VersionSet.parse("[1.5.0,3.0.0)", targets=["macos"])
        both = linux.union(macos)
        self.assertTrue(both.contains("1.2.0", target="linux"))
        self.assertTrue(both.contains("2.5.0", target="macos"))
        self.assertFalse(both.contains("1.2.0", target="macos"))
        inter = linux.intersect(macos)
        self.assertTrue(inter.is_empty())  # disjoint targets never meet


class SerializationTest(unittest.TestCase):
    def test_round_trip(self):
        s = VersionSet.parse("[1.0.0,2.0.0) || [3.0.0,4.0.0]",
                             channels=STABLE_B, targets=["linux"])
        back = VersionSet.from_dict(s.to_dict())
        self.assertEqual(back.canonical(), s.canonical())
        self.assertEqual(back, s)

    def test_hash_is_canonical_and_stable(self):
        s1 = VersionSet.parse("[1.0.0,2.0.0)").union(
            VersionSet.parse("[2.0.0,3.0.0)"))
        s2 = VersionSet.parse("[1.0.0,3.0.0)")
        self.assertEqual(s1, s2)
        self.assertEqual(s1.canonical(), s2.canonical())
        self.assertEqual(s1.digest(), s2.digest())
        self.assertEqual(s1.digest(), VersionSet.parse("[1.0.0,3.0.0)").digest())
        self.assertNotEqual(s1.digest(),
                            VersionSet.parse("[1.0.0,3.0.0]").digest())


if __name__ == "__main__":
    unittest.main()
