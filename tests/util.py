"""Shared helpers for the pwlin test-suite."""

from __future__ import annotations

import unittest

from pwlin import Interval


class PwlinTestCase(unittest.TestCase):
    def assertIntervalAlmostEqual(self, actual: Interval, lo: float, hi: float,
                                  places: int = 12) -> None:
        self.assertIsInstance(actual, Interval)
        self.assertAlmostEqual(actual.lo, lo, places=places,
                               msg=f"interval lo: {actual!r} != [{lo}, {hi}]")
        self.assertAlmostEqual(actual.hi, hi, places=places,
                               msg=f"interval hi: {actual!r} != [{lo}, {hi}]")
