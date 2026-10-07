import {
  Rational,
  cmp,
  formatRational,
  makeRational,
  mul,
  pow10,
  rational,
} from '../src/rational.js';
import { ScaleMode, createScale } from '../src/scale.js';
import { Tick } from '../src/ticks.js';
import { assert, assertRationalEqual, test } from './harness.js';

function assertWellFormed(ticks: Tick[], lo: Rational, hi: Rational, msg: string): void {
  assert(ticks.length > 0, `${msg}: at least one tick`);
  const labels = new Set<string>();
  for (let i = 0; i < ticks.length; i++) {
    const tick = ticks[i];
    assert(!labels.has(tick.label), `${msg}: duplicate label ${tick.label}`);
    labels.add(tick.label);
    assert(cmp(tick.value, lo) >= 0 && cmp(tick.value, hi) <= 0, `${msg}: tick ${tick.label} in domain`);
    assert(tick.label === formatRational(tick.value), `${msg}: label matches exact value`);
    if (i > 0) {
      assert(cmp(ticks[i - 1].value, tick.value) < 0, `${msg}: values strictly increasing`);
    }
  }
}

test('ticks: linear unit interval yields exact nice values', () => {
  const scale = createScale({ domain: [0, 1], range: [0, 100] });
  const ticks = scale.ticks(5);
  const expectedValues = [0, 1, 2, 3, 4, 5].map((k) => makeRational(BigInt(k), 5n));
  const expectedLabels = ['0', '0.2', '0.4', '0.6', '0.8', '1'];
  assert(ticks.length === expectedValues.length, `expected 6 ticks, got ${ticks.length}`);
  for (let i = 0; i < ticks.length; i++) {
    assertRationalEqual(ticks[i].value, expectedValues[i], `tick ${i} exact value`);
    assert(ticks[i].label === expectedLabels[i], `tick ${i} label is ${expectedLabels[i]}`);
  }
  assertRationalEqual(ticks[0].value, rational(0), 'includes low boundary');
  assertRationalEqual(ticks[ticks.length - 1].value, rational(1), 'includes high boundary');
});

test('ticks: log powers of ten are exact', () => {
  const scale = createScale({ domain: [1, 1000], mode: 'log', range: [0, 300] });
  const ticks = scale.ticks(4);
  const expected = [1n, 10n, 100n, 1000n];
  assert(ticks.length === expected.length, `expected 4 ticks, got ${ticks.length}`);
  for (let i = 0; i < ticks.length; i++) {
    assertRationalEqual(ticks[i].value, makeRational(expected[i]), `tick ${i} value`);
    assert(ticks[i].label === expected[i].toString(), `tick ${i} label`);
  }
});

test('ticks: log with denser request adds 2/5 mantissas', () => {
  const scale = createScale({ domain: [1, 1000], mode: 'log', range: [0, 300] });
  const ticks = scale.ticks(10);
  const labels = ticks.map((t) => t.label);
  const expected = ['1', '2', '5', '10', '20', '50', '100', '200', '500', '1000'];
  assert(
    labels.length === expected.length && labels.every((l, i) => l === expected[i]),
    `labels ${JSON.stringify(labels)}`,
  );
});

test('ticks: log sub-decade span falls back to linear-style ticks', () => {
  const scale = createScale({ domain: [2, 8], mode: 'log', range: [0, 100] });
  const ticks = scale.ticks(10);
  assertWellFormed(ticks, rational(2), rational(8), 'sub-decade');
  assert(ticks.length >= 3, 'enough ticks');
});

test('ticks: tiny spans still produce unique exact labels', () => {
  const lo = pow10(-30);
  const hi = mul(pow10(-30), makeRational(2n));
  const scale = createScale({ domain: [lo, hi], range: [0, 1000] });
  const ticks = scale.ticks(10);
  assertWellFormed(ticks, lo, hi, 'tiny linear span');
  assertRationalEqual(ticks[0].value, lo, 'starts at boundary');
  assertRationalEqual(ticks[ticks.length - 1].value, hi, 'ends at boundary');

  const logLo = rational(1);
  const logHi = makeRational(1000000001n, 1000000000n);
  const logScale = createScale({ domain: [logLo, logHi], mode: 'log', range: [0, 1000] });
  assertWellFormed(logScale.ticks(10), logLo, logHi, 'tiny log span');
});

test('ticks: uniqueness and monotonicity across many domains', () => {
  const linearDomains: Array<[Rational, Rational]> = [
    [rational(0), rational(1)],
    [makeRational(1n, 3n), makeRational(22n, 7n)],
    [makeRational(-5n), makeRational(-2n)],
    [pow10(-30), mul(pow10(-30), makeRational(2n))],
    [pow10(18), makeRational(10n ** 18n + 1n)],
  ];
  const logDomains: Array<[Rational, Rational]> = [
    [rational(1), pow10(6)],
    [pow10(-3), pow10(3)],
    [rational(2), rational(50)],
    [rational(1), makeRational(1000000001n, 1000000000n)],
  ];
  const cases: Array<{ mode: ScaleMode; domain: [Rational, Rational] }> = [
    ...linearDomains.map((domain) => ({ mode: 'linear' as ScaleMode, domain })),
    ...logDomains.map((domain) => ({ mode: 'log' as ScaleMode, domain })),
  ];
  for (const { mode, domain } of cases) {
    for (const range of [[0, 100], [100, 0]] as Array<[number, number]>) {
      for (const count of [4, 10, 25]) {
        const scale = createScale({ domain, range, mode });
        const ticks = scale.ticks(count);
        const msg = `${mode} [${formatRational(domain[0])}, ${formatRational(domain[1])}] count=${count}`;
        assertWellFormed(ticks, domain[0], domain[1], msg);
        const direction = range[0] < range[1] ? 1 : -1;
        for (let i = 1; i < ticks.length; i++) {
          const ok = direction === 1
            ? ticks[i].pixel > ticks[i - 1].pixel
            : ticks[i].pixel < ticks[i - 1].pixel;
          assert(ok, `${msg}: tick pixels monotonic at ${i}`);
        }
      }
    }
  }
});
