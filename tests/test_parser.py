import unittest
from fractions import Fraction

from chronodur import (Budget, BudgetExceeded, Duration, DurationOverflow,
                       ParseError, compile_program, parse_duration)


class TestBasicParsing(unittest.TestCase):
    def test_single_units(self):
        self.assertEqual(parse_duration("3mo"), Duration(months=3))
        self.assertEqual(parse_duration("2cd"), Duration(cal_days=2))
        self.assertEqual(parse_duration("1d"), Duration(fixed=Fraction(86400)))
        self.assertEqual(parse_duration("4h"), Duration(fixed=Fraction(14400)))
        self.assertEqual(parse_duration("5min"), Duration(fixed=Fraction(300)))
        self.assertEqual(parse_duration("6s"), Duration(fixed=Fraction(6)))

    def test_fixed_day_is_not_calendar_day(self):
        self.assertNotEqual(parse_duration("1d"), parse_duration("1cd"))
        self.assertEqual(parse_duration("1d").fixed, Fraction(86400))
        self.assertEqual(parse_duration("1cd").cal_days, 1)
        self.assertEqual(parse_duration("1cd").fixed, Fraction(0))

    def test_rational_seconds(self):
        self.assertEqual(parse_duration("1/3s").fixed, Fraction(1, 3))
        self.assertEqual(parse_duration("0.5s"), parse_duration("1/2s"))
        self.assertEqual(parse_duration("1.25h").fixed, Fraction(4500))
        self.assertEqual(parse_duration("2/4s").fixed, Fraction(1, 2))

    def test_adjacent_terms_sum(self):
        self.assertEqual(parse_duration("1h30min"), Duration(fixed=Fraction(5400)))
        self.assertEqual(parse_duration("1mo 2cd 3h"),
                         Duration(1, 2, Fraction(10800)))

    def test_operators_and_parens(self):
        self.assertEqual(parse_duration("1h + 30min - 10s").fixed, Fraction(5390))
        self.assertEqual(parse_duration("2 * (1mo 3h)"),
                         Duration(2, 0, Fraction(21600)))
        self.assertEqual(parse_duration("(1h 30min) * 2").fixed, Fraction(10800))
        self.assertEqual(parse_duration("1h30min - 30min").fixed, Fraction(3600))

    def test_leading_minus_negates_whole_expression(self):
        # A negative duration negates the whole, never just one field.
        self.assertEqual(parse_duration("-1h30min").fixed, Fraction(-5400))
        self.assertEqual(parse_duration("-(1mo 2cd)"), Duration(-1, -2))
        self.assertEqual(parse_duration("-1mo 2cd"), Duration(-1, -2))

    def test_scalar_arithmetic(self):
        self.assertEqual(parse_duration("3 * 2 * 1h").fixed, Fraction(21600))
        self.assertEqual(parse_duration("(1/2) * 4h").fixed, Fraction(7200))


class TestDiagnostics(unittest.TestCase):
    def assert_error_at(self, text, column0, exc=ParseError):
        with self.assertRaises(exc) as ctx:
            parse_duration(text)
        self.assertEqual(ctx.exception.start, column0,
                         f"wrong position for {text!r}: {ctx.exception}")
        return ctx.exception

    def test_ambiguous_bare_day(self):
        err = self.assert_error_at("1D", 1)
        self.assertIn("ambiguous", err.message)
        self.assertIn("cd", err.message)

    def test_ambiguous_m(self):
        err = self.assert_error_at("2m", 1)
        self.assertIn("ambiguous", err.message)

    def test_unknown_unit(self):
        self.assert_error_at("1x", 1)

    def test_bare_number_after_quantity(self):
        self.assert_error_at("1h 30", 3)

    def test_zero_denominator(self):
        self.assert_error_at("1/0s", 0)

    def test_unexpected_character(self):
        self.assert_error_at("1h @ 2h", 3)

    def test_unclosed_paren(self):
        self.assert_error_at("(1h", 3)

    def test_double_sign_rejected(self):
        self.assert_error_at("1h - -2h", 5)

    def test_multiply_two_durations(self):
        err = self.assert_error_at("2h * 3h", 0)
        self.assertIn("multiply", err.message)

    def test_mix_number_and_duration(self):
        err = self.assert_error_at("1 + 1h", 0)
        self.assertIn("bare number", err.message)

    def test_fractional_calendar_month(self):
        self.assert_error_at("1.5mo", 0)

    def test_fractional_calendar_scaling(self):
        self.assert_error_at("(1/2) * 1mo", 0)

    def test_bare_number_root(self):
        self.assert_error_at("42", 0)

    def test_empty_expression(self):
        self.assert_error_at("", 0)

    def test_caret_rendering_points_at_column(self):
        err = self.assert_error_at("1h 30", 3)
        rendered = str(err)
        self.assertIn("column 4", rendered)
        caret_line = rendered.splitlines()[-1]
        self.assertEqual(caret_line.index("^"), 5)  # 2-space indent + column 3


class TestBudget(unittest.TestCase):
    def test_depth_budget(self):
        src = "(" * 150 + "1s" + ")" * 150
        with self.assertRaises(BudgetExceeded):
            parse_duration(src)

    def test_step_budget(self):
        src = "1s+" * 500 + "1s"
        with self.assertRaises(BudgetExceeded):
            parse_duration(src, Budget(max_steps=100))

    def test_exponential_nesting_overflow_with_position(self):
        # 9**30 seconds explodes past the fixed-seconds limit; the error
        # must point inside the source, not crash or hang.
        src = "9*(" * 30 + "1s" + ")" * 30
        with self.assertRaises(DurationOverflow) as ctx:
            parse_duration(src)
        self.assertGreaterEqual(ctx.exception.start, 0)
        self.assertLess(ctx.exception.start, len(src))

    def test_denominator_overflow(self):
        with self.assertRaises(DurationOverflow):
            parse_duration("1/10000000000001s")

    def test_calendar_field_overflow(self):
        with self.assertRaises(DurationOverflow):
            parse_duration("1000000001mo")


class TestCompile(unittest.TestCase):
    def test_ops_preserve_source_order(self):
        self.assertEqual(compile_program("1mo 1d").ops,
                         (("mo", 1), ("fx", Fraction(86400))))
        self.assertEqual(compile_program("1d 1mo").ops,
                         (("fx", Fraction(86400)), ("mo", 1)))

    def test_scaling(self):
        self.assertEqual(compile_program("3 * (1mo 2cd 4h)").ops,
                         (("mo", 3), ("cd", 6), ("fx", Fraction(43200))))

    def test_subtraction_negates_ops(self):
        self.assertEqual(compile_program("1mo - 2cd").ops,
                         (("mo", 1), ("cd", -2)))

    def test_compose(self):
        p = compile_program("1mo").then(compile_program("2cd 3h"))
        self.assertEqual(p, compile_program("1mo 2cd 3h"))


if __name__ == "__main__":
    unittest.main()
