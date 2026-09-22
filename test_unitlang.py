import unittest

from unitlang.cli import run_source
from unitlang.errors import CalcError, DimError, EvalError, LexError, ParseError
from unitlang.lexer import tokenize


def run1(source):
    (out,) = run_source(source)
    return out


class ArithmeticTests(unittest.TestCase):
    def test_exact_rational_arithmetic(self):
        self.assertEqual(run1("0.1 + 0.2"), "0.3")
        self.assertEqual(run1("1 / 3 * 3"), "1")
        self.assertEqual(run1("2 / 4"), "0.5")
        self.assertEqual(run1("1 / 7"), "1/7")
        self.assertEqual(run1("2 * (3 + 4) - 5"), "9")

    def test_variables(self):
        self.assertEqual(run_source("x = 2\ny = x * 3\ny + 1"), ["7"])

    def test_unary_minus(self):
        self.assertEqual(run1("-(3 - 5)"), "2")


class UnitTests(unittest.TestCase):
    def test_add_with_conversion(self):
        self.assertEqual(run1("1 m + 20 cm"), "1.2 m")
        self.assertEqual(run1("1 h + 30 min"), "1.5 h")

    def test_explicit_conversion(self):
        self.assertEqual(run1("1 km to m"), "1000 m")
        self.assertEqual(run1("1 m to cm"), "100 cm")
        self.assertEqual(run1("2 h to min"), "120 min")
        self.assertEqual(run1("1 mi to km"), "1.609344 km")

    def test_compound_unit_conversion(self):
        self.assertEqual(run1("90 km / 1 h to m / s"), "25 m/s")
        self.assertEqual(run1("1 J to N * m"), "1 N*m")

    def test_derived_dimensions(self):
        self.assertEqual(run1("3 m * 2 m"), "6 m^2")
        self.assertEqual(run1("10 m / 4 s"), "2.5 m/s")
        self.assertEqual(run1("75 kg * 10 m / (1 s * 1 s) to N"), "750 N")

    def test_dimension_mismatch_rejected(self):
        with self.assertRaises(DimError) as ctx:
            run_source("1 m + 1 s")
        self.assertEqual(ctx.exception.span, (4, 5))
        self.assertIn("length", ctx.exception.message)
        self.assertIn("time", ctx.exception.message)

    def test_conversion_dimension_mismatch(self):
        with self.assertRaises(DimError):
            run_source("1 m to s")

    def test_analysis_happens_before_execution(self):
        with self.assertRaises(DimError):
            run_source("1 / 0\n1 m + 1 s")


class TemperatureTests(unittest.TestCase):
    def test_affine_arithmetic(self):
        self.assertEqual(run1("20 °C + 5 Δ°C"), "25 °C")
        self.assertEqual(run1("20 °C - 5 °C"), "15 K")
        self.assertEqual(run1("5 Δ°C + 20 °C"), "25 °C")

    def test_affine_conversion(self):
        self.assertEqual(run1("20 °C to °F"), "68 °F")
        self.assertEqual(run1("68 °F to °C"), "20 °C")
        self.assertEqual(run1("10 Δ°C to Δ°F"), "18 Δ°F")
        self.assertEqual(run1("0 K to °C"), "-273.15 °C")

    def test_absolute_temperature_cannot_be_multiplied(self):
        for src in ("2 °C * 3", "3 * 2 °C", "10 °C / 2", "2 °C * 3 °C"):
            with self.assertRaises(DimError, msg=src):
                run_source(src)

    def test_absolute_temperature_multiplication_span(self):
        with self.assertRaises(DimError) as ctx:
            run_source("1 °C * 2")
        self.assertEqual(ctx.exception.span, (6, 7))

    def test_cannot_add_two_absolute_temperatures(self):
        with self.assertRaises(DimError):
            run_source("1 °C + 2 °C")

    def test_absolute_plus_plain_rejected(self):
        with self.assertRaises(DimError):
            run_source("20 °C + 1 m")

    def test_affine_to_delta_conversion_rejected(self):
        with self.assertRaises(DimError):
            run_source("1 °C to Δ°C")

    def test_temperature_comparison(self):
        self.assertEqual(run1("0 °C == 32 °F"), "true")
        self.assertEqual(run1("20 °C < 21 °C"), "true")
        with self.assertRaises(DimError):
            run_source("1 °C == 1 Δ°C")


class ComparisonTests(unittest.TestCase):
    def test_cross_unit_comparison(self):
        self.assertEqual(run1("1 m == 100 cm"), "true")
        self.assertEqual(run1("100 cm == 1 m"), "true")
        self.assertEqual(run1("1 h > 30 min"), "true")
        self.assertEqual(run1("1 kg < 500 g"), "false")
        self.assertEqual(run1("1 m != 1 cm"), "true")

    def test_incompatible_comparison_rejected(self):
        with self.assertRaises(DimError):
            run_source("1 m < 1 s")


class RuntimeErrorTests(unittest.TestCase):
    def test_division_by_zero_locates_operator(self):
        with self.assertRaises(EvalError) as ctx:
            run_source("10 / 0")
        self.assertEqual(ctx.exception.span, (3, 4))
        self.assertIn("division by zero", ctx.exception.message)

    def test_division_by_zero_after_unit(self):
        with self.assertRaises(EvalError) as ctx:
            run_source("6 m / 0")
        self.assertEqual(ctx.exception.span, (4, 5))

    def test_division_by_computed_zero(self):
        with self.assertRaises(EvalError):
            run_source("1 / (2 - 2)")


class LexerTests(unittest.TestCase):
    def test_utf8_byte_spans(self):
        toks = tokenize("20 °C")
        self.assertEqual(
            [(t.kind, t.text, t.span) for t in toks],
            [
                ("NUMBER", "20", (0, 2)),
                ("IDENT", "°C", (3, 6)),
                ("EOF", "", (6, 6)),
            ],
        )

    def test_error_span_uses_bytes_not_chars(self):
        with self.assertRaises(DimError) as ctx:
            run_source("1 °C + 1 m")
        self.assertEqual(ctx.exception.span, (6, 7))

    def test_lex_error(self):
        with self.assertRaises(LexError):
            run_source("1 @ 2")

    def test_parse_error(self):
        with self.assertRaises(ParseError):
            run_source("1 +")
        with self.assertRaises(ParseError):
            run_source("1 lightyear")

    def test_undefined_variable(self):
        with self.assertRaises(CalcError):
            run_source("x + 1")

    def test_comments_and_blank_lines(self):
        src = "# comment\n\nx = 1  # trailing\n\nx + 1\n"
        self.assertEqual(run_source(src), ["2"])


if __name__ == "__main__":
    unittest.main()
