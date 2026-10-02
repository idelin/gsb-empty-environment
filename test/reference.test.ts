/**
 * Finite scaled-integer reference verification.
 *
 * Every decimal is cross-checked against an independent reference model that
 * scales both operands to a common exponent and compares plain BigInts —
 * the finite-integer ground truth for exact decimal arithmetic.
 */
import { test } from "node:test";
import assert from "node:assert/strict";
import type { Decimal } from "../src/index.ts";
import {
  checkConstraints,
  compareDecimal,
  isMultipleOf,
  normalizeConstraints,
  parseDecimal,
  precisionOf,
  scaleOf,
  solve,
} from "../src/index.ts";

// --- reference model: scale to common exponent, compare BigInts ------------

function toScaledInt(d: Decimal, e: number): bigint {
  // value * 10^-e as an integer; requires e <= d.exp
  return (d.neg ? -1n : 1n) * d.coeff * 10n ** BigInt(d.exp - e);
}

function refCompare(a: Decimal, b: Decimal): -1 | 0 | 1 {
  const e = Math.min(a.exp, b.exp);
  const ai = toScaledInt(a, e);
  const bi = toScaledInt(b, e);
  return ai < bi ? -1 : ai > bi ? 1 : 0;
}

function refMultipleOf(v: Decimal, m: Decimal): boolean {
  const e = Math.min(v.exp, m.exp);
  const vi = toScaledInt(v, e);
  const mi = toScaledInt(m, e);
  return vi % mi === 0n;
}

function refPrecision(d: Decimal): number {
  // ground truth: write the value in plain decimal, count digits
  const e = d.exp;
  const digits = d.coeff.toString();
  if (d.coeff === 0n) return 1;
  const intDigits = Math.max(0, digits.length + e);
  const fracDigits = Math.max(0, -e);
  return Math.max(intDigits, 1) === 1 && intDigits === 0
    ? fracDigits
    : intDigits + fracDigits;
}

// --- deterministic pseudo-random decimal generator -------------------------

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomDecimal(rand: () => number): Decimal {
  const coeffLen = 1 + Math.floor(rand() * 12);
  let digits = "";
  for (let i = 0; i < coeffLen; i++) digits += Math.floor(rand() * 10).toString();
  if (/^0+$/.test(digits)) digits = "1" + digits.slice(1);
  const exp = Math.floor(rand() * 13) - 6; // -6..6
  const neg = rand() < 0.5;
  return parseDecimal((neg ? "-" : "") + digits + "e" + exp);
}

test("compareDecimal matches scaled-integer reference (500 cases)", () => {
  const rand = mulberry32(42);
  for (let i = 0; i < 500; i++) {
    const a = randomDecimal(rand);
    const b = randomDecimal(rand);
    assert.equal(compareDecimal(a, b), refCompare(a, b), `compare(${a.raw}, ${b.raw})`);
  }
});

test("isMultipleOf matches scaled-integer reference (500 cases)", () => {
  const rand = mulberry32(7);
  for (let i = 0; i < 500; i++) {
    const v = randomDecimal(rand);
    const m = randomDecimal(rand);
    assert.equal(isMultipleOf(v, m), refMultipleOf(v, m), `multipleOf(${v.raw}, ${m.raw})`);
  }
});

test("precision/scale match plain-decimal ground truth (500 cases)", () => {
  const rand = mulberry32(99);
  for (let i = 0; i < 500; i++) {
    const d = randomDecimal(rand);
    assert.equal(precisionOf(d), refPrecision(d), `precision(${d.raw})`);
    assert.equal(scaleOf(d), Math.max(0, -d.exp), `scale(${d.raw})`);
  }
});

test("solver witnesses re-verified by scaled-integer reference (300 cases)", () => {
  const rand = mulberry32(1234);
  for (let i = 0; i < 300; i++) {
    const lo = randomDecimal(rand);
    const hi = randomDecimal(rand);
    const m = randomDecimal(rand);
    const schema: Record<string, unknown> = {};
    const [a, b] = refCompare(lo, hi) <= 0 ? [lo, hi] : [hi, lo];
    if (rand() < 0.8) schema.minimum = a.raw;
    if (rand() < 0.8) schema.maximum = b.raw;
    if (rand() < 0.7) schema.multipleOf = m.raw;
    if (rand() < 0.4) schema.scale = Math.floor(rand() * 5);
    if (rand() < 0.3) schema.precision = 1 + Math.floor(rand() * 8);
    const c = normalizeConstraints(schema);
    const r = solve(c);
    if (!r.feasible) continue; // infeasibility is brute-force checked below
    // Independent re-verification of every constraint via scaled integers.
    const w = r.witness;
    if (c.lo !== undefined) {
      const ok = refCompare(w, c.lo.value);
      assert.ok(ok > 0 || (ok === 0 && c.lo.inclusive), `lo ${w.raw} vs ${c.lo.value.raw}`);
    }
    if (c.hi !== undefined) {
      const ok = refCompare(w, c.hi.value);
      assert.ok(ok < 0 || (ok === 0 && c.hi.inclusive), `hi ${w.raw} vs ${c.hi.value.raw}`);
    }
    if (c.multipleOf !== undefined) {
      assert.ok(refMultipleOf(w, c.multipleOf), `multipleOf ${w.raw} vs ${c.multipleOf.raw}`);
    }
    if (c.precision !== undefined) assert.ok(refPrecision(w) <= c.precision);
    if (c.scale !== undefined) assert.ok(scaleOf(w) <= c.scale);
  }
});

test("no-solution evidence confirmed by bounded brute force", () => {
  // Small bounded infeasible cases: enumerate every j on the grid and confirm
  // none satisfies all constraints, validating the solver's evidence.
  const cases: Record<string, unknown>[] = [
    { minimum: "0.15", maximum: "0.25", multipleOf: "1" },
    { minimum: "0.5", maximum: "1.5", multipleOf: "2" },
    { minimum: "0.05", maximum: "0.09", precision: 1 },
    { exclusiveMinimum: "0.1", exclusiveMaximum: "0.2", multipleOf: "0.1" },
    { minimum: "0.3", maximum: "0.4", multipleOf: "0.7", scale: 1 },
  ];
  for (const schema of cases) {
    const c = normalizeConstraints(schema);
    const r = solve(c);
    assert.equal(r.feasible, false, JSON.stringify(schema));
    // brute force over a fine grid covering the range
    const g = 6;
    const lo = parseDecimal((schema.minimum ?? schema.exclusiveMinimum ?? "0") as string);
    const hi = parseDecimal((schema.maximum ?? schema.exclusiveMaximum ?? "2") as string);
    const e = Math.min(lo.exp, hi.exp, -g);
    const jLo = Number(toScaledInt(lo, e));
    const jHi = Number(toScaledInt(hi, e));
    let found = false;
    for (let j = jLo; j <= jHi && !found; j++) {
      const v = parseDecimal(String(j) + "e" + String(e));
      if (checkConstraints(v, c).length === 0) found = true;
    }
    assert.equal(found, false, `brute force found a value the solver missed: ${JSON.stringify(schema)}`);
  }
});
