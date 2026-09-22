import subprocess
import sys
import unittest
from pathlib import Path

from unitlang import run
from unitlang.errors import LangError
from unitlang.lexer import tokenize

ROOT = Path(__file__).resolve().parent.parent


def out(src):
    return run(src)


def one(src):
    lines = run(src)
    assert len(lines) == 1, lines
    return lines[0]


class LexerTests(unittest.TestCase):
    def test_utf8_byte_spans(self):
        toks = tokenize("20 °C\n")
        self.assertEqual((toks[0].start, toks[0].end), (0, 2))
        self.assertEqual(toks[1].text, "°C")
        self.assertEqual((toks[1].start, toks[1].end), (3, 6))  # ° 占 2 字节

    def test_unknown_char(self):
        with self.assertRaises(LangError):
            tokenize("1 @ 2")


class ArithmeticTests(unittest.TestCase):
    def test_exact_fraction(self):
        self.assertEqual(one("1/3 + 1/6"), "0.5")

    def test_decimal_is_exact(self):
        self.assertEqual(one("0.1 + 0.2"), "0.3")  # 精确有理数，不是 0.30000000000000004

    def test_repeating_fraction(self):
        self.assertEqual(one("1/3"), "1/3 (≈ 0.333333)")

    def test_precedence_and_parens(self):
        self.assertEqual(one("2 + 3 * 4"), "14")
        self.assertEqual(one("(2 + 3) * 4"), "20")

    def test_unary_minus(self):
        self.assertEqual(one("-3 + 5"), "2")


class UnitTests(unittest.TestCase):
    def test_add_same_dimension(self):
        self.assertEqual(one("1 km + 5 m"), "1.005 km")

    def test_length_plus_time_rejected(self):
        with self.assertRaises(LangError) as ctx:
            run("1 m + 1 s")
        self.assertIn("量纲不同", ctx.exception.message)
        self.assertEqual((ctx.exception.start, ctx.exception.end), (4, 5))  # 定位到 +

    def test_operator_span_with_multibyte_units(self):
        with self.assertRaises(LangError) as ctx:
            run("1 °C + 1 s")
        self.assertEqual((ctx.exception.start, ctx.exception.end), (6, 7))

    def test_compound_unit_literal(self):
        self.assertEqual(one("9.8 m/s^2"), "9.8 m/s^2")

    def test_scalar_times_quantity_keeps_unit(self):
        self.assertEqual(one("2 * 3 km"), "6 km")

    def test_product_falls_back_to_base_units(self):
        self.assertEqual(one("1 km * 1 s"), "1000 m*s")

    def test_conversions(self):
        self.assertEqual(one("1 mi -> km"), "1.609344 km")
        self.assertEqual(one("1 in -> cm"), "2.54 cm")
        self.assertEqual(one("1 h -> min"), "60 min")
        self.assertEqual(one("10 m/s -> km/h"), "36 km/h")

    def test_conversion_dimension_mismatch(self):
        with self.assertRaises(LangError) as ctx:
            run("1 m -> s")
        self.assertIn("无法换算", ctx.exception.message)

    def test_unknown_unit(self):
        with self.assertRaises(LangError) as ctx:
            run("5 xyz")
        self.assertIn("未知单位", ctx.exception.message)


class TemperatureTests(unittest.TestCase):
    def test_absolute_minus_absolute_is_delta(self):
        self.assertEqual(one("20 °C - 15 °C"), "5 Δ°C")

    def test_absolute_plus_delta(self):
        self.assertEqual(one("20 °C + 5 Δ°C"), "25 °C")

    def test_affine_conversions(self):
        self.assertEqual(one("100 °C -> °F"), "212 °F")
        self.assertEqual(one("32 °F -> °C"), "0 °C")
        self.assertEqual(one("100 °C -> K"), "373.15 K")
        self.assertEqual(one("373.15 K -> °C"), "100 °C")

    def test_delta_conversion(self):
        self.assertEqual(one("9 Δ°F -> Δ°C"), "5 Δ°C")

    def test_absolute_plus_absolute_rejected(self):
        with self.assertRaises(LangError) as ctx:
            run("20 °C + 20 °C")
        self.assertIn("不能相加", ctx.exception.message)

    def test_absolute_multiply_rejected(self):
        for src in ("2 * 20 °C", "20 °C * 2", "20 °C / 2"):
            with self.assertRaises(LangError, msg=src) as ctx:
                run(src)
            self.assertIn("乘除", ctx.exception.message)

    def test_delta_multiply_allowed(self):
        self.assertEqual(one("2 * 5 Δ°C"), "10 Δ°C")

    def test_absolute_to_delta_rejected(self):
        with self.assertRaises(LangError):
            run("20 °C -> Δ°C")

    def test_delta_to_absolute_rejected(self):
        with self.assertRaises(LangError):
            run("5 Δ°C -> °C")

    def test_negative_literal_temperature(self):
        self.assertEqual(one("-5 °C"), "-5 °C")

    def test_negating_absolute_variable_rejected(self):
        with self.assertRaises(LangError):
            run("let t = 20 °C\n-t")


class CompareTests(unittest.TestCase):
    def test_compare_converts_units(self):
        self.assertEqual(one("1 km > 999 m"), "true")
        self.assertEqual(one("1 mi >= 1.6 km"), "true")

    def test_compare_temperatures_across_scales(self):
        self.assertEqual(one("20 °C == 68 °F"), "true")

    def test_compare_dimension_mismatch(self):
        with self.assertRaises(LangError) as ctx:
            run("1 m < 1 s")
        self.assertIn("不能比较", ctx.exception.message)

    def test_compare_absolute_with_delta_rejected(self):
        with self.assertRaises(LangError):
            run("20 °C > 10 Δ°C")


class DivisionByZeroTests(unittest.TestCase):
    def test_span_points_at_operator(self):
        with self.assertRaises(LangError) as ctx:
            run("1 / 0")
        self.assertIn("除数为零", ctx.exception.message)
        self.assertEqual((ctx.exception.start, ctx.exception.end), (2, 3))

    def test_zero_from_computation(self):
        with self.assertRaises(LangError) as ctx:
            run("1 m / (1 s - 1 s)")
        self.assertIn("除数为零", ctx.exception.message)


class VariableTests(unittest.TestCase):
    def test_let_and_use(self):
        self.assertEqual(
            out("let x = 3 m\nlet y = 2\nx * y"),
            ["x = 3 m", "y = 2", "6 m"],
        )

    def test_undefined_variable(self):
        with self.assertRaises(LangError) as ctx:
            run("x + 1")
        self.assertIn("未定义的变量", ctx.exception.message)


class StaticCheckTests(unittest.TestCase):
    def test_dimensions_checked_before_execution(self):
        # 第一条语句若执行会除零；量纲检查先于执行，应报量纲错误
        with self.assertRaises(LangError) as ctx:
            run("1 / 0\n1 m + 1 s\n")
        self.assertIn("量纲不同", ctx.exception.message)


DEMO_EXPECTED = [
    "0.5",
    "0.3",
    "distance = 42 km",
    "time = 1.5 h",
    "70/9 (≈ 7.77778) m/s",
    "28 km/h",
    "g = 9.8 m/s^2",
    "t = 3 s",
    "29.4 m/s",
    "1.609344 km",
    "2.54 cm",
    "60 min",
    "morning = 8 °C",
    "noon = 21 °C",
    "13 Δ°C",
    "13 °C",
    "69.8 °F",
    "373.15 K",
    "0 °C",
    "5 Δ°C",
    "true",
    "true",
    "true",
]


class DemoTests(unittest.TestCase):
    def test_demo_output(self):
        src = (ROOT / "examples" / "demo.ul").read_text(encoding="utf-8")
        self.assertEqual(run(src), DEMO_EXPECTED)

    def test_cli_demo(self):
        proc = subprocess.run(
            [sys.executable, "-m", "unitlang", "examples/demo.ul"],
            capture_output=True,
            text=True,
            cwd=ROOT,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(proc.stdout.strip().splitlines(), DEMO_EXPECTED)

    def test_cli_error_exit_code_and_caret(self):
        proc = subprocess.run(
            [sys.executable, "-m", "unitlang"],
            input="1 m + 1 s\n",
            capture_output=True,
            text=True,
            cwd=ROOT,
        )
        self.assertEqual(proc.returncode, 1)
        self.assertIn("量纲不同", proc.stderr)
        self.assertIn("^", proc.stderr)


if __name__ == "__main__":
    unittest.main()
