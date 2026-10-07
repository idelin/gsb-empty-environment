import test from 'node:test';
import assert from 'node:assert/strict';
import {
  add,
  cmp,
  createScale,
  eq,
  mul,
  rat,
  sub,
  toNumber,
  type Rational,
  type Scale,
  type Tick,
} from '../src/index.ts';

const TINY_LO = rat(1);
const TINY_HI = rat('1.000000001'); // span = 1e-9

function samples(lo: Rational, hi: Rational, n: number): Rational[] {
  const out: Rational[] = [];
  for (let i = 0; i <= n; i += 1) {
    out.push(add(lo, mul(sub(hi, lo), rat(BigInt(i), BigInt(n)))));
  }
  return out;
}

function relErr(actual: Rational, expected: Rational): number {
  const denom = Math.max(Math.abs(toNumber(expected)), 1e-300);
  return Math.abs(toNumber(sub(actual, expected))) / denom;
}

function assertWellFormedTicks(scale: Scale, ticks: readonly Tick[]): void {
  const labels = ticks.map((t) => t.label);
  assert.equal(new Set(labels).size, labels.length, 'tick labels must not repeat');
  const [d0, d1] = scale.domain;
  const lo = cmp(d0, d1) < 0 ? d0 : d1;
  const hi = cmp(d0, d1) < 0 ? d1 : d0;
  for (let i = 0; i < ticks.length; i += 1) {
    const { value, label } = ticks[i];
    assert.equal(typeof label, 'string');
    assert.ok(label.length > 0, 'label must be non-empty');
    assert.ok(cmp(value, lo) >= 0, `tick ${label} below domain`);
    assert.ok(cmp(value, hi) <= 0, `tick ${label} above domain`);
    if (i > 0) {
      assert.ok(cmp(ticks[i - 1].value, value) < 0, 'tick values must strictly increase');
    }
  }
}

test('linear: map hits pixel endpoints exactly', () => {
  const s = createScale({ domain: [rat(1, 3), rat(1000, 7)], range: [100, 800] });
  assert.equal(s.map(rat(1, 3)), 100);
  assert.equal(s.map(rat(1000, 7)), 800);
});

test('linear: invert hits domain endpoints exactly (exact rationals)', () => {
  const d0 = rat(1, 3);
  const d1 = rat(1000, 7);
  const s = createScale({ domain: [d0, d1], range: [100, 800] });
  assert.ok(eq(s.invert(100), d0), 'invert(p0) must equal d0 exactly');
  assert.ok(eq(s.invert(800), d1), 'invert(p1) must equal d1 exactly');
});

test('linear: round-trip map->invert restores values', () => {
  const lo = rat(1, 3);
  const hi = rat(1000, 7);
  const s = createScale({ domain: [lo, hi], range: [100, 800] });
  for (const v of samples(lo, hi, 50)) {
    assert.ok(relErr(s.invert(s.map(v)), v) < 1e-12, `round-trip failed for ${toNumber(v)}`);
  }
});

test('linear: round-trip holds over a 1e-9 span', () => {
  const s = createScale({ domain: [TINY_LO, TINY_HI], range: [0, 700] });
  for (const v of samples(TINY_LO, TINY_HI, 20)) {
    assert.ok(relErr(s.invert(s.map(v)), v) < 1e-9, `tiny-span round-trip failed`);
  }
});

test('log: constructor rejects non-positive domain', () => {
  assert.throws(() => createScale({ domain: [rat(0), rat(10)], mode: 'log', range: [0, 1] }), RangeError);
  assert.throws(() => createScale({ domain: [rat(-5), rat(10)], mode: 'log', range: [0, 1] }), RangeError);
  assert.throws(
    () => createScale({ domain: [rat(-5), rat(-1)], mode: 'log', range: [0, 1] }),
    /positive domain/,
  );
});

test('log: map explicitly rejects non-positive values', () => {
  const s = createScale({ domain: [rat('0.01'), rat(1000)], mode: 'log', range: [0, 500] });
  assert.throws(() => s.map(rat(0)), /non-positive/);
  assert.throws(() => s.map(rat(-3)), /non-positive/);
  assert.throws(() => s.map(rat('-0.5')), RangeError);
  assert.equal(s.mode, 'log');
});

test('linear: non-positive values are fine', () => {
  const s = createScale({ domain: [rat(-10), rat(10)], range: [0, 200] });
  assert.equal(s.map(rat(-10)), 0);
  assert.equal(s.map(rat(0)), 100);
});

test('log: round-trip map->invert restores values', () => {
  const lo = rat('0.001');
  const hi = rat(10000);
  const s = createScale({ domain: [lo, hi], mode: 'log', range: [0, 700] });
  assert.equal(s.map(lo), 0);
  assert.equal(s.map(hi), 700);
  for (const v of samples(lo, hi, 40)) {
    if (cmp(v, rat(0)) <= 0) continue;
    assert.ok(relErr(s.invert(s.map(v)), v) < 1e-9, `log round-trip failed for ${toNumber(v)}`);
  }
  assert.ok(relErr(s.invert(0), lo) < 1e-9, 'invert(p0) must recover d0');
  assert.ok(relErr(s.invert(700), hi) < 1e-9, 'invert(p1) must recover d1');
});

test('monotonicity: linear and log, normal and inverted pixel ranges', () => {
  const cases: ReadonlyArray<readonly [Scale, Rational, Rational, 1 | -1]> = [
    [createScale({ domain: [rat(0), rat(10)], range: [0, 100] }), rat(0), rat(10), 1],
    [createScale({ domain: [rat(0), rat(10)], range: [100, 0] }), rat(0), rat(10), -1],
    [createScale({ domain: [rat('0.01'), rat(1000)], mode: 'log', range: [0, 500] }), rat('0.01'), rat(1000), 1],
    [createScale({ domain: [rat('0.01'), rat(1000)], mode: 'log', range: [500, 0] }), rat('0.01'), rat(1000), -1],
    [createScale({ domain: [TINY_LO, TINY_HI], range: [700, 0] }), TINY_LO, TINY_HI, -1],
  ];
  for (const [scale, lo, hi, direction] of cases) {
    const pts = samples(lo, hi, 100);
    for (let i = 1; i < pts.length; i += 1) {
      const prev = scale.map(pts[i - 1]);
      const next = scale.map(pts[i]);
      assert.ok(
        direction * (next - prev) > 0,
        `map must be strictly monotonic (${scale.mode}, dir ${direction}): ${prev} -> ${next}`,
      );
    }
  }
});

test('inverted pixel range is legal and exact at boundaries', () => {
  const s = createScale({ domain: [rat(0), rat(10)], range: [500, -100] });
  assert.equal(s.map(rat(0)), 500);
  assert.equal(s.map(rat(10)), -100);
  assert.ok(eq(s.invert(500), rat(0)));
  assert.ok(eq(s.invert(-100), rat(10)));
});

test('clip is separate from map/invert and handles inverted ranges', () => {
  const s = createScale({ domain: [rat(0), rat(10)], range: [0, 100] });
  assert.equal(s.map(rat(20)), 200); // map does not clamp
  assert.equal(s.clip(200), 100);
  assert.equal(s.clip(-50), 0);
  assert.ok(eq(s.invert(s.clip(s.map(rat(20)))), rat(10)));

  const inv = createScale({ domain: [rat(0), rat(10)], range: [100, 0] });
  assert.equal(inv.map(rat(20)), -100);
  assert.equal(inv.clip(-100), 0);
  assert.equal(inv.clip(150), 100);
  assert.ok(eq(inv.invert(inv.clip(inv.map(rat(20)))), rat(10)));
});

test('degenerate inputs are rejected', () => {
  assert.throws(() => createScale({ domain: [rat(1), rat(1)], range: [0, 100] }), /endpoints must differ/);
  assert.throws(() => createScale({ domain: [rat(0), rat(1)], range: [50, 50] }), /endpoints must differ/);
  assert.throws(() => createScale({ domain: [rat(0), rat(1)], range: [0, Number.NaN] }), /finite/);
});

test('ticks: exact rational values on [0, 1]', () => {
  const s = createScale({ domain: [rat(0), rat(1)], range: [0, 100] });
  const ticks = s.ticks(5);
  const expected = [rat(0), rat(1, 5), rat(2, 5), rat(3, 5), rat(4, 5), rat(1)];
  assert.equal(ticks.length, expected.length);
  ticks.forEach((t, i) => {
    assert.ok(eq(t.value, expected[i]), `tick ${i} must be exactly ${expected[i].n}/${expected[i].d}`);
  });
  assert.deepEqual(ticks.map((t) => t.label), ['0', '0.2', '0.4', '0.6', '0.8', '1']);
});

test('ticks: unique labels and in-domain values over a 1e-9 span', () => {
  const s = createScale({ domain: [TINY_LO, TINY_HI], range: [0, 700] });
  const ticks = s.ticks(10);
  assert.ok(ticks.length >= 5, 'tiny span should still produce ticks');
  assertWellFormedTicks(s, ticks);
});

test('ticks: log mode falls back to linear steps inside a narrow span', () => {
  const lo = rat('1.5');
  const hi = rat('1.5001');
  const s = createScale({ domain: [lo, hi], mode: 'log', range: [0, 300] });
  const ticks = s.ticks(10);
  assert.ok(ticks.length >= 3, 'narrow log span should fall back to linear ticks');
  assertWellFormedTicks(s, ticks);
});

test('ticks: log decades are exact powers of ten', () => {
  const s = createScale({ domain: [rat('0.01'), rat(1000)], mode: 'log', range: [0, 500] });
  const ticks = s.ticks();
  assertWellFormedTicks(s, ticks);
  for (const power of ['0.01', '0.1', '1', '10', '100', '1000']) {
    assert.ok(
      ticks.some((t) => eq(t.value, rat(power))),
      `expected an exact tick at ${power}`,
    );
  }
  for (const t of ticks) {
    assert.ok(t.value.n > 0n, 'log ticks must be positive');
  }
});

test('ticks: labels never repeat across many domains and modes', () => {
  const scales: Scale[] = [
    createScale({ domain: [rat(0), rat(1)], range: [0, 100] }),
    createScale({ domain: [rat(-7, 3), rat(22, 7)], range: [0, 400] }),
    createScale({ domain: [TINY_HI, TINY_LO], range: [0, 700] }), // inverted domain
    createScale({ domain: [rat('0.001'), rat(10000)], mode: 'log', range: [0, 700] }),
    createScale({ domain: [rat('0.999'), rat('1.001')], mode: 'log', range: [0, 200] }),
  ];
  for (const s of scales) {
    for (const count of [3, 5, 10, 20]) {
      assertWellFormedTicks(s, s.ticks(count));
    }
  }
});
