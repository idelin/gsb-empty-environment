"""A runnable tour of chronodur. Run from the repo root:

    python3.11 examples/demo.py
"""
import datetime as dt
import sys
from fractions import Fraction
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from chronodur import (Budget, BudgetExceeded, DurationOverflow, ParseError,
                       SyntheticZone, Transition, WallTime, compile_program,
                       parse_duration)

EPOCH = dt.datetime(1970, 1, 1)
HOUR = 3600


def utc_seconds(y, mo, d, h=0, mi=0):
    delta = dt.datetime(y, mo, d, h, mi) - EPOCH
    return delta.days * 86400 + delta.seconds


def us_style_zone():
    return SyntheticZone("US-like", [
        Transition(utc_seconds(2024, 3, 10, 7), -4 * HOUR, "EDT"),
        Transition(utc_seconds(2024, 11, 3, 6), -5 * HOUR, "EST"),
    ], base_offset_seconds=-5 * HOUR)


def section(title):
    print(f"\n== {title} ==")


def main():
    section("1. Parsing and canonical (round-trip) form")
    for text in ["1h30min", "90s", "0.5s", "1mo 2cd 3h", "24h", "30cd",
                 "-1h30min", "1mo - 3h", "2*(1mo 4h)"]:
        value = parse_duration(text)
        print(f"  {text!r:>18}  ->  {value}   (re-parses equal: "
              f"{parse_duration(str(value)) == value})")

    section("2. Fixed 24h day vs calendar day across an offset change")
    zone = us_style_zone()
    base = WallTime(dt.datetime(2024, 3, 9, 12, 0), zone)
    for text in ["1cd", "1d"]:
        result = compile_program(text).apply(base)
        delta = (result.to_instant() - base.to_instant()).fixed
        print(f"  2024-03-09 12:00 + {text:>3} -> wall {result.dt}  "
              f"(elapsed {delta}s = {delta / 3600}h)")

    section("3. Non-commutation counterexample: calendar month vs fixed day")
    a = compile_program("1mo 1d").apply(base)
    b = compile_program("1d 1mo").apply(base)
    print(f"  base 2024-03-09 12:00 US-like")
    print(f"  +1mo then +1d -> {a.dt}")
    print(f"  +1d then +1mo -> {b.dt}")
    print(f"  equal? {a == b}  (values agree, ordered programs do not)")

    section("4. Month-end clamping (not reversible)")
    for text in ["1mo", "2mo", "3mo"]:
        result = compile_program(text).apply(
            WallTime(dt.datetime(2024, 1, 31, 9, 30), zone))
        print(f"  2024-01-31 + {text} -> {result.dt.date()}")

    section("5. Program composition")
    p = compile_program("1mo").then(compile_program("1cd 12h"))
    print(f"  (1mo).then(1cd 12h) ops = {p.ops}")
    print(f"  applied to 2024-01-31 09:30 -> "
          f"{p.apply(WallTime(dt.datetime(2024, 1, 31, 9, 30), zone)).dt}")

    section("6. Exact rational seconds")
    base2 = WallTime(dt.datetime(2024, 6, 1, 12, 0), zone, Fraction(1, 3))
    result = compile_program("1/2s").apply(base2)
    print(f"  sub-second 1/3 + 1/2 = {result.sub} (exact, no floats)")

    section("7. Diagnostics with exact positions")
    for bad in ["1D", "1h 30", "2h * 3h", "1/0s", "1.5mo", "1h - -2h"]:
        try:
            parse_duration(bad)
        except ParseError as exc:
            print("  " + str(exc).replace("\n", "\n  "))

    section("8. Budgets: nesting depth and exponential blowup")
    try:
        parse_duration("(" * 150 + "1s" + ")" * 150)
    except BudgetExceeded as exc:
        print(f"  deep nesting -> {exc.message}")
    try:
        parse_duration("9*(" * 30 + "1s" + ")" * 30)
    except DurationOverflow as exc:
        print(f"  9^30 nesting -> {exc.message} at column {exc.start + 1}")


if __name__ == "__main__":
    main()
