import datetime as dt
import unittest
from fractions import Fraction

from chronodur import (Duration, WallTime, compile_program, parse_duration)

from tests.helpers import us_style_zone

EXPRESSIONS = [
    "1mo", "2cd", "1d", "90s", "1/3s", "0.5s", "1mo 2cd 3d 4h 5min 6s",
    "-(1mo 30min)", "1mo - 3h", "3h - 1mo", "0s", "1000000d", "7/8s",
    "1cd - 1s", "2*(3mo 4h)", "1mo 1d", "1d 1mo", "86400s", "24h",
    "30cd", "744h", "-(2cd 1/2s)", "1h30min", "-1h30min",
]

BASES = [
    dt.datetime(2024, 1, 31, 9, 15),    # month-end clamp target
    dt.datetime(2024, 2, 29, 23, 59),   # leap day
    dt.datetime(2024, 3, 9, 12, 0),     # just before spring forward
    dt.datetime(2024, 3, 10, 12, 0),    # just after spring forward
    dt.datetime(2024, 11, 2, 12, 0),    # just before fall back
    dt.datetime(2023, 12, 31, 0, 0),    # year boundary
]


class TestParsePrintRoundTrip(unittest.TestCase):
    def test_round_trip_and_idempotence(self):
        for text in EXPRESSIONS:
            with self.subTest(text=text):
                value = parse_duration(text)
                canonical = str(value)
                self.assertEqual(parse_duration(canonical), value)
                self.assertEqual(str(parse_duration(canonical)), canonical)

    def test_generated_values_round_trip(self):
        for months in (-2, 0, 1, 13):
            for cal_days in (-5, 0, 40):
                for fixed in (Fraction(0), Fraction(1, 3),
                              Fraction(-90061), Fraction(86400 * 400)):
                    value = Duration(months, cal_days, fixed)
                    with self.subTest(value=value):
                        canonical = str(value)
                        self.assertEqual(parse_duration(canonical), value)
                        self.assertEqual(str(parse_duration(canonical)),
                                         canonical)


class TestProgramsAcrossSyntheticZone(unittest.TestCase):
    """Round-trip canonical text, then apply the compiled program at bases
    straddling offset transitions and month ends."""

    def setUp(self):
        self.zone = us_style_zone()

    def test_apply_at_tricky_bases(self):
        for text in EXPRESSIONS:
            canonical = str(parse_duration(text))
            program = compile_program(canonical)
            for base_dt in BASES:
                with self.subTest(text=canonical, base=base_dt):
                    base = WallTime(base_dt, self.zone, Fraction(1, 7))
                    result = program.apply(base)
                    self.assertEqual(result.zone, self.zone)
                    # The exact sub-second part survives calendar-only ops.
                    if all(op[0] != "fx" for op in program.ops):
                        self.assertEqual(result.sub, Fraction(1, 7))
                    # Result must resolve back to an instant (no gaps hit).
                    result.to_instant()

    def test_normalization_is_idempotent_under_application(self):
        # Normalizing the *value* twice changes nothing, and the canonical
        # program is a fixed point of parse -> print -> compile.
        for text in EXPRESSIONS:
            with self.subTest(text=text):
                canonical = str(parse_duration(text))
                self.assertEqual(compile_program(canonical).ops,
                                 compile_program(str(parse_duration(canonical))).ops)


if __name__ == "__main__":
    unittest.main()
