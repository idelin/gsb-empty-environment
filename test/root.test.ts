import { test } from 'node:test';
import assert from 'node:assert/strict';
import { rootN, verifyRoundedRoot } from '../src/root.ts';
import { parseDecimal, formatDecimal, normalizeDecimal } from '../src/decimal.ts';
import type { RoundingMode } from '../src/root.ts';

const MODES: RoundingMode[] = ['floor', 'ceil', 'half-up', 'half-down', 'half-even'];

test('perfect powers are exact (sqrt fast path and nth root)', () => {
  const a = rootN('144', 2, 5);
  assert.equal(a.coeff, 1200000n);
  assert.equal(a.exact, true);
  assert.equal(formatDecimal(a), '12.00000');
  const b = rootN('152399025', 2, 4); // 12345^2
  assert.equal(b.coeff, 123450000n);
  assert.equal(b.exact, true);
  const c = rootN('1024', 10, 3); // 2^10
  assert.equal(c.coeff, 2000n);
  assert.equal(c.exact, true);
  const sq = 987654321n ** 2n;
  const d = rootN(sq, 2, 0);
  assert.equal(d.coeff, 987654321n);
  assert.equal(d.exact, true);
});

test('known digits: sqrt(2) and cbrt(2)', () => {
  assert.equal(formatDecimal(rootN('2', 2, 10)), '1.4142135624');
  assert.equal(
    formatDecimal(rootN('2', 2, 50)),
    '1.41421356237309504880168872420969807856967187537695',
  );
  assert.equal(formatDecimal(rootN('2', 3, 20)), '1.25992104989487316477');
});

test('midpoint ties resolved by exact integer power comparison (half-even)', () => {
  // sqrt(6.25) = 2.5 exactly -> tie -> even neighbour 2
  assert.equal(rootN('6.25', 2, 0).coeff, 2n);
  assert.equal(rootN('6.25', 2, 0, { mode: 'half-up' }).coeff, 3n);
  assert.equal(rootN('6.25', 2, 0, { mode: 'half-down' }).coeff, 2n);
  assert.equal(rootN('6.25', 2, 0, { mode: 'ceil' }).coeff, 3n);
  assert.equal(rootN('20.25', 2, 0).coeff, 4n); // 4.5 -> 4
  assert.equal(rootN('30.25', 2, 0).coeff, 6n); // 5.5 -> 6
  assert.equal(rootN('3.375', 3, 0).coeff, 2n); // cbrt = 1.5 -> 2
  assert.equal(rootN('42.875', 3, 0).coeff, 4n); // cbrt = 3.5 -> 4
  assert.equal(rootN('97.65625', 5, 0).coeff, 2n); // 5th root = 2.5 -> 2
  assert.equal(rootN('0.25', 2, 0).coeff, 0n); // 0.5 -> 0 (even)
  assert.equal(rootN('0.25', 2, 0, { mode: 'half-up' }).coeff, 1n);
  // just off the midpoint
  assert.equal(rootN('6.26', 2, 0).coeff, 3n);
  assert.equal(rootN('6.24', 2, 0).coeff, 2n);
});

test('exhaustive midpoint ties r+1/2 for n=2,3 and r=1..50', () => {
  for (let r = 1n; r <= 50n; r++) {
    const expected = r % 2n === 0n ? r : r + 1n;
    const odd2 = 2n * r + 1n;
    // ((2r+1)/2)^2 = (2r+1)^2 / 4 = 25*(2r+1)^2 * 10^-2
    const x2 = { coeff: odd2 * odd2 * 25n, exp: -2 };
    const res2 = rootN(x2, 2, 0);
    assert.equal(res2.coeff, expected, `sqrt tie at ${r}.5`);
    assert.ok(verifyRoundedRoot(x2, 2, 0, res2));
    // ((2r+1)/2)^3 = (2r+1)^3 / 8 = 125*(2r+1)^3 * 10^-3
    const x3 = { coeff: odd2 * odd2 * odd2 * 125n, exp: -3 };
    const res3 = rootN(x3, 3, 0);
    assert.equal(res3.coeff, expected, `cbrt tie at ${r}.5`);
    assert.ok(verifyRoundedRoot(x3, 3, 0, res3));
  }
});

test('tiny inputs are not truncated to zero', () => {
  const tiny = rootN('0.00000000009', 2, 10);
  assert.equal(formatDecimal(tiny), '0.0000094868');
  assert.notEqual(tiny.coeff, 0n);
  // input far below output scale: a truncating implementation would yield 0
  const tinier = rootN('0.0000000000000000000000005', 2, 20);
  assert.equal(formatDecimal(tinier), '0.00000000000070710678');
  assert.ok(verifyRoundedRoot('0.0000000000000000000000005', 2, 20, tinier));
  // exact tiny power: sqrt(1e-1000) = 1e-500
  const exactTiny = rootN({ coeff: 1n, exp: -1000 }, 2, 600);
  assert.equal(exactTiny.coeff, 10n ** 100n);
  assert.equal(exactTiny.exact, true);
});

test('negative input rejected, zero and n=1 handled', () => {
  assert.throws(() => rootN('-4', 2, 2), RangeError);
  assert.throws(() => rootN('-0.5', 3, 2), RangeError);
  assert.throws(() => rootN({ coeff: -1n, exp: 0 }, 2, 0), RangeError);
  assert.equal(rootN('-0', 2, 2).coeff, 0n); // -0 is not negative
  const z = rootN('0', 3, 5);
  assert.equal(z.coeff, 0n);
  assert.equal(z.exact, true);
  assert.equal(rootN('2.5', 1, 0).coeff, 2n);
  assert.equal(rootN('3.5', 1, 0).coeff, 4n);
  assert.equal(rootN('2.51', 1, 0).coeff, 3n);
});

test('input and output precision are independent', () => {
  const ref = rootN('2', 2, 10);
  for (const s of ['2.000', '2e0', '0.0002e4', '20e-1', '200.00e-2']) {
    assert.equal(rootN(s, 2, 10).coeff, ref.coeff, s);
  }
  // extra input digits genuinely change the rounded output
  const hi = rootN('2.00000000000000000000000000000000000000000000000001', 2, 60);
  const lo = rootN('2', 2, 60);
  assert.ok(hi.coeff > lo.coeff);
});

test('integer floor/ceil bracket surrounds the scaled root', () => {
  const r = rootN('2', 2, 0);
  assert.equal(r.floor, 1n);
  assert.equal(r.ceil, 2n);
  const exact = rootN('144', 2, 3);
  assert.equal(exact.floor, 12000n);
  assert.equal(exact.ceil, 12000n);
});

test('digit budget is enforced', () => {
  assert.throws(() => rootN('2', 2, 200, { maxDigits: 100 }), RangeError);
  assert.throws(() => rootN('9'.repeat(500), 2, 0, { maxDigits: 100 }), RangeError);
  const ok = rootN('2', 2, 50, { maxDigits: 200 });
  assert.equal(ok.coeff, 141421356237309504880168872420969807856967187537695n);
});

test('exhaustive verification: integers 0..1000, scales 0..3, n in {2,3}, all modes', () => {
  for (let t = 0n; t <= 1000n; t++) {
    for (let scale = 0; scale <= 3; scale++) {
      for (const n of [2, 3]) {
        for (const mode of MODES) {
          const res = rootN(t, n, scale, { mode });
          assert.ok(verifyRoundedRoot(t, n, scale, res), `t=${t} n=${n} s=${scale} ${mode}`);
        }
      }
    }
  }
});

test('exhaustive verification: fractional inputs m/100, m=1..300, E<0 path', () => {
  for (let m = 1n; m <= 300n; m++) {
    const x = { coeff: m, exp: -2 };
    for (const mode of MODES) {
      const res = rootN(x, 2, 4, { mode });
      assert.ok(verifyRoundedRoot(x, 2, 4, res), `m=${m} ${mode}`);
    }
  }
});

test('verification rejects tampered results', () => {
  const samples: [string, number, number][] = [['2', 2, 10], ['6.25', 2, 0], ['0.00000000009', 2, 10], ['12345', 3, 5]];
  for (const [s, n, scale] of samples) {
    const good = rootN(s, n, scale);
    assert.ok(verifyRoundedRoot(s, n, scale, good));
    const bad = { ...good, coeff: good.coeff + 1n };
    assert.equal(verifyRoundedRoot(s, n, scale, bad), false, `tamper +1 accepted for ${s}`);
    if (good.coeff > 0n) {
      const bad2 = { ...good, coeff: good.coeff - 1n };
      assert.equal(verifyRoundedRoot(s, n, scale, bad2), false, `tamper -1 accepted for ${s}`);
    }
  }
});

test('result square/power bounds: floor mode brackets x * 10^(n*s)', () => {
  for (const s of ['2', '3.14159', '0.00000000009', '123456789123456789']) {
    for (const n of [2, 3, 5]) {
      const res = rootN(s, n, 8, { mode: 'floor' });
      const x = parseDecimal(s);
      const lhs = res.coeff ** BigInt(n);
      const rhs = x.coeff * 10n ** BigInt(x.exp + n * 8);
      assert.ok(lhs <= rhs);
      assert.ok((res.coeff + 1n) ** BigInt(n) > rhs);
    }
  }
});

test('recovery: format/parse round-trip keeps the same rounded result', () => {
  const samples = ['2', '0.00000000009', '6.25', '152399025', '1.5e3', '123456789123456789.000125'];
  for (const s of samples) {
    const direct = rootN(s, 2, 12);
    const recovered = parseDecimal(formatDecimal(parseDecimal(s)));
    const again = rootN(recovered, 2, 12);
    assert.equal(again.coeff, direct.coeff, s);
    // result itself survives a format/parse cycle unchanged
    const rt = parseDecimal(formatDecimal(direct));
    assert.deepEqual(normalizeDecimal(rt), normalizeDecimal({ coeff: direct.coeff, exp: direct.exp }));
  }
});

test('huge operands within budget', () => {
  const big = 10n ** 120n + 1n;
  const res = rootN(big, 2, 10);
  assert.ok(verifyRoundedRoot(big, 2, 10, res));
  assert.equal(res.floor, 10n ** 70n); // floor(sqrt(10^120+1) * 10^10) = 10^70
  const huge = rootN({ coeff: 1n, exp: -1000 }, 2, 600);
  assert.ok(verifyRoundedRoot({ coeff: 1n, exp: -1000 }, 2, 600, huge));
});
