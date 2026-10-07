import {
  Rational,
  RationalLike,
  add,
  cmp,
  makeRational,
  mul,
  pow10,
  rational,
  sub,
  toNumber,
} from '../src/rational.js';
import {
  DegenerateScaleError,
  LogDomainError,
  ScaleMode,
  createScale,
} from '../src/scale.js';
import {
  assert,
  assertClose,
  assertRationalEqual,
  assertThrows,
  lcg,
  test,
} from './harness.js';

function sampleDomain(d0: Rational, d1: Rational, t: Rational): Rational {
  return add(d0, mul(sub(d1, d0), t));
}

function assertMonotonic(pixels: number[], direction: 1 | -1, msg: string): void {
  for (let i = 1; i < pixels.length; i++) {
    const ok = direction === 1 ? pixels[i] > pixels[i - 1] : pixels[i] < pixels[i - 1];
    assert(ok, `${msg}: not strictly monotonic at index ${i} (${pixels[i - 1]} -> ${pixels[i]})`);
  }
}

function samplePixels(
  mode: ScaleMode,
  domain: [Rational, Rational],
  range: [number, number],
  samples: number,
): number[] {
  const scale = createScale({ domain, range, mode });
  const pixels: number[] = [];
  for (let i = 0; i <= samples; i++) {
    const t = makeRational(BigInt(i), BigInt(samples));
    pixels.push(scale.map(sampleDomain(domain[0], domain[1], t)));
  }
  return pixels;
}

test('linear: endpoints map to pixel range exactly', () => {
  const scale = createScale({ domain: [0, 1], range: [0, 100] });
  assert(scale.map(0) === 0, 'low endpoint');
  assert(scale.map(1) === 100, 'high endpoint');
  assert(scale.map(0.5) === 50, 'midpoint');
  assertRationalEqual(scale.invert(0), rational(0), 'invert low endpoint');
  assertRationalEqual(scale.invert(100), rational(1), 'invert high endpoint');
});

test('linear: inverted pixel range is legal and monotonic decreasing', () => {
  const scale = createScale({ domain: [0, 10], range: [500, 100] });
  assert(scale.map(0) === 500 && scale.map(10) === 100, 'endpoints flip');
  const pixels = samplePixels('linear', [rational(0), rational(10)], [500, 100], 200);
  assertMonotonic(pixels, -1, 'inverted range');
});

test('linear: inverted domain is legal', () => {
  const scale = createScale({ domain: [10, 0], range: [0, 100] });
  assert(scale.map(10) === 0 && scale.map(0) === 100, 'endpoints');
  assertRationalEqual(scale.clamp(15), rational(10), 'clamp uses sorted domain');
  assertRationalEqual(scale.clamp(-3), rational(0), 'clamp low');
});

test('linear: invert does not clamp; clamp is a separate operation', () => {
  const scale = createScale({ domain: [0, 1], range: [0, 100] });
  const above = scale.invert(150);
  assert(cmp(above, rational(1)) > 0, 'invert(150) lies above the domain');
  assertRationalEqual(above, rational(1.5), 'invert(150) is exactly 1.5');
  assertRationalEqual(scale.clamp(above), rational(1), 'clamp pulls it back');
  const below = scale.invert(-50);
  assert(cmp(below, rational(0)) < 0, 'invert(-50) lies below the domain');
  assertRationalEqual(scale.clamp(below), rational(0), 'clamp low');
  assert(scale.clampPixel(150) === 100 && scale.clampPixel(-5) === 0, 'clampPixel');
  assert(scale.clampPixel(40) === 40, 'clampPixel keeps interior values');
});

test('linear: degenerate inputs are rejected', () => {
  assertThrows(
    () => createScale({ domain: [1, 1], range: [0, 100] }),
    DegenerateScaleError,
    'equal domain endpoints',
  );
  assertThrows(
    () => createScale({ domain: [0, 1], range: [50, 50] }),
    DegenerateScaleError,
    'equal pixel endpoints',
  );
});

function assertLinearRoundTrip(
  domain: [RationalLike, RationalLike],
  range: [number, number],
  samples: number,
  msg: string,
): void {
  const scale = createScale({ domain, range });
  const d0 = rational(domain[0]);
  const d1 = rational(domain[1]);
  const span = Math.abs(toNumber(sub(d1, d0)));
  const rand = lcg(12345);
  for (let i = 0; i < samples; i++) {
    const v = sampleDomain(d0, d1, rational(rand()));
    const back = scale.invert(scale.map(v));
    const err = Math.abs(toNumber(sub(back, v)));
    assert(err <= 1e-9 * span, `${msg}: round-trip error ${err} (span ${span})`);
  }
}

test('linear: round-trip across ordinary and awkward domains', () => {
  assertLinearRoundTrip([0, 1], [0, 100], 200, 'unit interval');
  assertLinearRoundTrip([makeRational(1n, 3n), makeRational(22n, 7n)], [-50, 320], 200, 'fractional');
  assertLinearRoundTrip([-5, -2], [100, 0], 200, 'negative with inverted range');
});

test('linear: round-trip and monotonicity for tiny spans', () => {
  assertLinearRoundTrip([pow10(-30), mul(pow10(-30), makeRational(2n))], [0, 1000], 200, '1e-30 span');
  assertLinearRoundTrip([pow10(-300), mul(pow10(-300), makeRational(2n))], [0, 1000], 200, '1e-300 span');
  const pixels = samplePixels(
    'linear',
    [pow10(-30), mul(pow10(-30), makeRational(2n))],
    [0, 1000],
    500,
  );
  assertMonotonic(pixels, 1, 'tiny span monotonicity');
});

test('log: non-positive domains are explicitly rejected', () => {
  assertThrows(
    () => createScale({ domain: [0, 10], mode: 'log', range: [0, 100] }),
    LogDomainError,
    'zero endpoint',
  );
  assertThrows(
    () => createScale({ domain: [-5, 10], mode: 'log', range: [0, 100] }),
    LogDomainError,
    'negative endpoint',
  );
  assertThrows(
    () => createScale({ domain: [-5, -1], mode: 'log', range: [0, 100] }),
    LogDomainError,
    'both negative',
  );
});

test('log: non-positive input values are explicitly rejected', () => {
  const scale = createScale({ domain: [1, 100], mode: 'log', range: [0, 200] });
  assertThrows(() => scale.map(0), LogDomainError, 'map(0)');
  assertThrows(() => scale.map(makeRational(-3n, 2n)), LogDomainError, 'map(negative)');
  assertThrows(() => scale.clamp(0), LogDomainError, 'clamp(0)');
});

test('log: endpoints map to pixel range exactly', () => {
  const scale = createScale({ domain: [1, 1e6], mode: 'log', range: [0, 600] });
  assert(scale.map(1) === 0, 'low endpoint');
  assert(scale.map(1e6) === 600, 'high endpoint');
  assertClose(scale.map(1000), 300, 1e-9, 'midpoint in log space');
  assertRationalEqual(scale.invert(0), rational(1), 'invert low endpoint');
  assertRationalEqual(scale.invert(600), rational(1e6), 'invert high endpoint');
});

function assertLogRoundTrip(
  domain: [RationalLike, RationalLike],
  range: [number, number],
  samples: number,
  msg: string,
): void {
  const scale = createScale({ domain, range, mode: 'log' });
  const d0 = rational(domain[0]);
  const d1 = rational(domain[1]);
  const logSpan = Math.abs(Math.log(toNumber(d1) / toNumber(d0)));
  const rand = lcg(777);
  for (let i = 0; i < samples; i++) {
    const v = sampleDomain(d0, d1, rational(rand()));
    const back = scale.invert(scale.map(v));
    const err = Math.abs(Math.log(toNumber(back) / toNumber(v)));
    assert(err <= Math.max(1e-9 * logSpan, 1e-12), `${msg}: log-space round-trip error ${err}`);
  }
}

test('log: round-trip across wide and tiny spans', () => {
  assertLogRoundTrip([makeRational(1n, 1000n), pow10(6)], [0, 500], 200, 'wide span');
  assertLogRoundTrip([1, makeRational(1000000001n, 1000000000n)], [0, 1000], 200, '1e-9 relative span');
});

test('log: monotonicity, including inverted range and tiny span', () => {
  assertMonotonic(
    samplePixels('log', [rational(1), pow10(6)], [0, 600], 500),
    1,
    'normal range',
  );
  assertMonotonic(
    samplePixels('log', [rational(1), pow10(6)], [600, 0], 500),
    -1,
    'inverted range',
  );
  assertMonotonic(
    samplePixels('log', [rational(1), makeRational(1000000001n, 1000000000n)], [0, 1000], 500),
    1,
    'tiny span',
  );
});
