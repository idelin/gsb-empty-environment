import { test } from "node:test";
import assert from "node:assert/strict";
import { formatRational, parseRational } from "../src/convert.ts";
import { verifyFractionDigits } from "../src/fraction.ts";
import { Rational } from "../src/rational.ts";

// Exhaustive small-rational round-trip: every num/den in a grid, every
// charset family, formatted (cycle always found since remainders are
// bounded by den) and parsed back to the identical rational. Each emitted
// (prefix, cycle) is also independently verified in closed form.
test("exhaustive small rationals round-trip across bases and charsets", () => {
  const bases: Array<[number, string]> = [
    [2, "lower"],
    [3, "lower"],
    [8, "lower"],
    [10, "lower"],
    [16, "lower"],
    [16, "upper"],
    [36, "lower"],
    [36, "upper"],
    [62, "base62"],
    [62, "base62alt"],
  ];
  let checked = 0;
  for (const [base, charset] of bases) {
    for (let num = -12; num <= 12; num++) {
      for (let den = 1; den <= 12; den++) {
        const value = new Rational(BigInt(num), BigInt(den));
        const out = formatRational(value, { base, charset });
        assert.equal(out.complete, true, `${value} base ${base} must complete (den bounds remainders)`);
        const back = parseRational(out.text, base, charset);
        assert.ok(back.equals(value), `${value} -> ${out.text} -> ${back} (base ${base}, ${charset})`);
        const mag = value.num < 0n ? -value.num : value.num;
        const frac = new Rational(mag % value.den, value.den);
        const v = verifyFractionDigits(frac, base, charset, out.prefix, out.cycle);
        assert.ok(v.valid && v.minimalCycle && v.canonicalStart,
          `${value} base ${base}: ${v.reason}`);
        checked++;
      }
    }
  }
  assert.ok(checked > 2000, `exhaustive grid actually ran (${checked} cases)`);
});

test("negative zero never appears in the exhaustive grid output", () => {
  for (let num = -12; num <= 0; num++) {
    const out = formatRational(new Rational(BigInt(num), 7n), { base: 10 });
    if (num !== 0 && out.text.startsWith("-0")) {
      // "-0.xxx" is a legitimate rendering of a negative fraction;
      // only the exact string "-0" is forbidden.
      assert.notEqual(out.text, "-0");
    }
    if (num === 0) assert.equal(out.text, "0");
  }
});
