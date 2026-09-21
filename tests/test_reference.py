"""Randomised differential test against a point-sampling brute force."""

import random
import unittest
from fractions import Fraction

from coverage_ledger import Ledger, query


def brute_force(view, lo, hi, mode, k=None):
    """Exact reference: union each device's records, then sweep all endpoints."""
    from coverage_ledger import intervals as iv

    raw_member_rules = {
        d: [(r[2], hi if r[3] is None else min(r[3], hi))
            for r in view.membership.raw(d)
            if r[2] < hi and (r[3] is None or r[3] > lo)]
        for d in view.membership.devices()
    }
    online = {
        d: iv.clip([(r[2], r[3]) for r in view.observations.raw(d)], (lo, hi))
        for d in raw_member_rules
    }
    points = {lo, hi}
    for ranges in online.values():
        for s, e in ranges:
            points.update((s, e))
    for rules in raw_member_rules.values():
        for s, e in rules:
            points.update((max(s, lo), min(e, hi)))
    total = 0
    for a, b in zip(sorted(points), sorted(points)[1:]):
        mid = (a + b) / 2
        online_now = {d for d, ranges in online.items()
                      if any(s <= mid < e for s, e in ranges)}
        members_now = {d for d, rules in raw_member_rules.items()
                       if any(s <= mid < e for s, e in rules)}
        both = online_now & members_now
        if mode == "union":
            ok = bool(both)
        elif mode == "intersection":
            ok = bool(members_now) and both == members_now
        else:
            ok = len(both) >= k
        total += b - a if ok else 0
    return Fraction(total, hi - lo)


class ReferenceTests(unittest.TestCase):
    def test_random_histories_match_brute_force(self):
        rng = random.Random(20260921)
        devices = ["A", "B", "C", "D"]
        for trial in range(40):
            led = Ledger()
            # membership rule versions, including one leave (closed rule)
            for d in devices:
                if rng.random() < 0.8:
                    end = None if rng.random() < 0.7 else rng.randint(3, 9)
                    led.add_membership(d, rng.choice([0, 1, 2]), end)
            # overlapping and duplicate (different id, same range) reports
            obs_count = 0
            for _ in range(rng.randint(3, 12)):
                d = rng.choice(devices)
                a = rng.randint(0, 9)
                b = a + rng.randint(1, 4)
                obs_count += 1
                led.add_observation(d, a, b, observation_id=f"o-{trial}-{obs_count}")
                if rng.random() < 0.2:
                    obs_count += 1
                    led.add_observation(d, a, b, observation_id=f"o-{trial}-{obs_count}-dup")

            lo, hi = 0, 10
            for mode, k in (("union", None), ("intersection", None),
                            ("at_least_k", 1), ("at_least_k", 2), ("at_least_k", 3)):
                result = query(led.snapshot(), (lo, hi), mode, k)
                self.assertEqual(result.window_length, hi - lo)
                expected_rate = brute_force(led.snapshot(), lo, hi, mode, k)
                self.assertEqual(
                    result.coverage_rate, expected_rate,
                    msg=f"trial {trial} mode {mode} k {k}: "
                        f"{result.coverage_rate} != {expected_rate}",
                )
                # covered elementary segments are disjoint and total to length
                segs = [(s.start, s.end) for s in result.covered_segments]
                self.assertEqual(Fraction(result.known_coverage_length, 10),
                                 result.coverage_rate)
                merged = __import__("coverage_ledger.intervals", fromlist=["merge"]).merge(segs)
                self.assertEqual(sum(e - s for s, e in merged),
                                 result.known_coverage_length)


if __name__ == "__main__":
    unittest.main()
