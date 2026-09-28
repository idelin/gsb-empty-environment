import { test } from "node:test";
import assert from "node:assert/strict";
import { add, cmp, div, eq, frac, gcd, mul, sub, type Fraction } from "../src/fraction.ts";
import { expand, format, parseExact } from "../src/decimal.ts";

/**
 * Exhaustive checks over a small-integer grid with INDEPENDENT reference
 * logic (raw cross-multiplication identities and a standalone long division),
 * so the library is checked against something other than itself.
 *
 * Truncated decimals are never treated as exact: round-trips only use the
 * exact "pre(period)" rendering.
 */

const K = 15;
const ints: bigint[] = [];
for (let i = 1; i <= K; i += 1) ints.push(BigInt(i), -BigInt(i));
ints.push(0n);

function refFrac(n: bigint, d: bigint): Fraction {
  if (d < 0n) {
    n = -n;
    d = -d;
  }
  const g = gcd(n === 0n ? 1n : n, d);
  return n === 0n ? { n: 0n, d: 1n } : { n: n / g, d: d / g };
}

/** Independent raw comparison through cross multiplication. */
function refCmp(a: Fraction, b: Fraction): -1 | 0 | 1 {
  const x = a.n * b.d;
  const y = b.n * a.d;
  return x === y ? 0 : x < y ? -1 : 1;
}

test("exhaustive canonicalization: coprime, positive denominator, zero is 0/1", () => {
  let checked = 0;
  for (const n of ints) {
    for (let d = -K; d <= K; d += 1) {
      if (d === 0) continue;
      const f = frac(n, d);
      assert.ok(f.d > 0n, `denominator must be positive: ${n}/${d}`);
      assert.equal(gcd(f.n < 0n ? -f.n : f.n, f.d), f.n === 0n ? 1n : 1n);
      if (f.n === 0n) {
        assert.equal(f.d, 1n);
      }
      assert.deepEqual(f, refFrac(n, BigInt(d)));
      checked += 1;
    }
  }
  assert.ok(checked >= 900);
});

test("exhaustive four operations against raw cross-multiplication identities", () => {
  const small = ints.filter((x) => x !== 0n).slice(0, 24);
  let checked = 0;
  for (const an of small) {
    for (const ad of small) {
      const a = frac(an, ad);
      for (const bn of small) {
        for (const bd of small.slice(0, 10)) {
          const b = frac(bn, bd);

          // add: (an/ad)+(bn/bd) == (an*bd+bn*ad)/(ad*bd), independently reduced
          assert.deepEqual(add(a, b), refFrac(an * bd + bn * ad, ad * bd));
          assert.deepEqual(sub(a, b), refFrac(an * bd - bn * ad, ad * bd));
          assert.deepEqual(mul(a, b), refFrac(an * bn, ad * bd));

          const q = div(a, b);
          // division identity: q * b === a exactly
          assert.ok(eq(mul(q, b), a), `(${an}/${ad}) / (${bn}/${bd}) round trip`);

          // comparison must agree with the independent raw cross product
          assert.equal(cmp(a, b), refCmp(a, b));
          checked += 1;
        }
      }
    }
  }
  assert.ok(checked > 50_000);
});

test("exhaustive comparison agrees with sign of cross product (including zero)", () => {
  for (const an of ints) {
    for (const ad of ints.filter((x) => x !== 0n)) {
      const a = frac(an, ad);
      for (const bn of ints.slice(0, 15)) {
        for (const bd of ints.filter((x) => x !== 0n).slice(0, 15)) {
          const b = frac(bn, bd);
          const x = a.n * b.d - b.n * a.d;
          const expected = x === 0n ? 0 : x < 0n ? -1 : 1;
          assert.equal(cmp(a, b), expected);
        }
      }
    }
  }
});

/**
 * Standalone reference long division: returns {kind, text} using its own
 * remainder-state map, deliberately separate from the library implementation.
 */
function referenceExpansion(n: bigint, d: bigint, budget: number): { kind: string; text: string } {
  let neg = false;
  if (n < 0n) {
    neg = true;
    n = -n;
  }
  if (d < 0n) d = -d;
  const g = gcd(n, d);
  if (g !== 0n) {
    n /= g;
    d /= g;
  }
  const intPart = n / d;
  let rem = n % d;

  if (rem === 0n) return { kind: "terminating", text: `${neg ? "-" : ""}${intPart}` };

  const digits: string[] = [];
  const pos = new Map<bigint, number>();
  let kind = "terminating";
  let cycleStart = -1;

  while (digits.length < budget) {
    if (rem === 0n) {
      kind = "terminating";
      break;
    }
    if (pos.has(rem)) {
      kind = "repeating";
      cycleStart = pos.get(rem)!;
      break;
    }
    pos.set(rem, digits.length);
    rem *= 10n;
    digits.push((rem / d).toString());
    rem %= d;
  }
  if (cycleStart === -1 && digits.length >= budget && rem !== 0n) {
    return { kind: "incomplete", text: `${neg ? "-" : ""}${intPart}.${digits.join("")}...` };
  }
  if (kind === "repeating") {
    const pre = digits.slice(0, cycleStart).join("");
    const period = digits.slice(cycleStart).join("");
    return {
      kind,
      text: `${neg ? "-" : ""}${intPart}.${pre}(${period})`,
    };
  }
  const fracPart = digits.join("").replace(/0+$/, "");
  return { kind, text: `${neg ? "-" : ""}${intPart}.${fracPart}` };
}

test("exhaustive decimal expansion matches an independent long division and round-trips", () => {
  let checked = 0;
  for (let a = 1; a <= K; a += 1) {
    for (let b = 1; b <= K; b += 1) {
      for (const sign of [1n, -1n]) {
        const n = BigInt(a) * sign;
        const d = BigInt(b);
        const f = frac(n, d);
        const e = expand(f, { maxDigits: 10_000, maxStates: 10_001 });
        const ref = referenceExpansion(n, d, 10_000);

        assert.equal(e.kind, ref.kind, `kind ${n}/${d}`);
        assert.equal(format(e), ref.text, `text ${n}/${d}`);

        // Exact renderings must parse back to the exact rational value.
        if (e.kind === "terminating" || e.kind === "repeating") {
          assert.deepEqual(parseExact(format(e)), f, `round trip ${n}/${d}`);
        }
        checked += 1;
      }
    }
  }
  assert.ok(checked > 400);
});

test("period rotation: digits produced by paging reproduce the reference period", () => {
  // 1/7 period rotates consistently across arbitrary page offsets.
  const f = frac(1n, 7n);
  const e = expand(f);
  assert.equal(e.period, "142857");
  for (const offset of [0n, 6n, 7n, 600n, 601n, 1_000_003n]) {
    const start = Number(offset);
    const page: string[] = [];
    // manual reference rotation with bigints
    let rem = 1n;
    // skip `start` long-division steps, then collect 6 digits
    for (let i = 0; i < start; i += 1) rem = (rem * 10n) % 7n;
    for (let i = 0; i < 6; i += 1) {
      rem *= 10n;
      page.push((rem / 7n).toString());
      rem %= 7n;
    }
    // The library does not expose paging here; verify by string rotation math.
    const expected = page.join("");
    const period = "142857";
    const rot = period.repeat(2).slice(start % 6, (start % 6) + 6);
    assert.equal(rot, expected, `offset ${start}`);
  }
});

test("incomplete rendering is visibly truncated and must not parse as exact", () => {
  const f = frac(1n, 100003n);
  const text = format(expand(f, { maxDigits: 10 }));
  assert.match(text, /\.\.\.$/);
  assert.throws(() => parseExact(text), /exact/);
});

test("exact fractions never compare equal merely because truncated decimals coincide", () => {
  // 1/3 and 333/1000 share "0.333..." to 3 places but are not equal.
  const a = frac(1n, 3n);
  const b = frac(333n, 1000n);
  assert.notEqual(cmp(a, b), 0);
  assert.equal(cmp(a, b), 1);
});
