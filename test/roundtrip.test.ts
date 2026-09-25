import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  Pipeline,
  adaptationMatrix,
  mat3Apply,
  rgbToXyzMatrix,
  srgbSpace,
  prophotoSpace,
  srgbTransfer,
  whiteToXYZ,
  D65,
  D50,
  type Vec3,
} from '../src/index.ts';
import { standardCompiledGraph, standardGraph, approxVec3 } from './helpers.ts';

const TOLERANCE = 1e-9;

const REFERENCE_COLORS: Vec3[] = [
  [0, 0, 0], // black
  [1, 1, 1], // white
  [1, 0, 0], // red primary
  [0, 1, 0], // green primary
  [0, 0, 1], // blue primary
  [0.5, 0.5, 0.5], // mid gray
  [0.25, 0.5, 0.75],
];

test('black/white/primaries round-trip srgb -> xyz -> srgb within tolerance', () => {
  const graph = standardCompiledGraph();
  const forward = new Pipeline(graph, { source: 'srgb', target: 'xyz', strategy: 'preserve' });
  const backward = new Pipeline(graph, { source: 'xyz', target: 'srgb', strategy: 'preserve' });
  for (const color of REFERENCE_COLORS) {
    const xyz = forward.convertPixel(...color).rgb;
    const back = backward.convertPixel(...xyz).rgb;
    assert.ok(
      approxVec3(back, color, TOLERANCE),
      `round trip of ${color} gave ${back}`,
    );
  }
});

test('black/white/primaries round-trip srgb -> display-p3 -> srgb within tolerance', () => {
  const graph = standardCompiledGraph();
  const forward = new Pipeline(graph, { source: 'srgb', target: 'display-p3', strategy: 'preserve' });
  const backward = new Pipeline(graph, { source: 'display-p3', target: 'srgb', strategy: 'preserve' });
  for (const color of REFERENCE_COLORS) {
    const there = forward.convertPixel(...color).rgb;
    const back = backward.convertPixel(...there).rgb;
    assert.ok(approxVec3(back, color, TOLERANCE), `round trip of ${color} gave ${back}`);
  }
});

test('negative linear components follow the sign-preserving definition, never NaN', () => {
  for (const value of [-2, -0.4, -0.0031308, -1e-6, 0, 1e-6, 0.5, 3]) {
    const encoded = srgbTransfer.encode(value);
    assert.ok(Number.isFinite(encoded), `encode(${value}) not finite`);
    const decoded = srgbTransfer.decode(encoded);
    assert.ok(Math.abs(decoded - value) <= 1e-12, `round trip of ${value} gave ${decoded}`);
    if (value < 0) assert.ok(encoded < 0, 'negative linear must encode negative');
  }
});

test('out-of-gamut negative linear pixel survives preserve round trip without NaN', () => {
  const graph = standardCompiledGraph();
  const forward = new Pipeline(graph, { source: 'srgb-linear', target: 'srgb', strategy: 'preserve' });
  const backward = new Pipeline(graph, { source: 'srgb', target: 'srgb-linear', strategy: 'preserve' });
  const pixel: Vec3 = [1.2, -0.35, 0.5];
  const encoded = forward.convertPixel(...pixel).rgb;
  for (const v of encoded) assert.ok(Number.isFinite(v), 'encoded component not finite');
  assert.ok(encoded[1] < 0, 'negative linear green must stay negative when preserved');
  const back = backward.convertPixel(...encoded).rgb;
  assert.ok(approxVec3(back, pixel, 1e-9), `round trip gave ${back}`);
});

test('theoretically equivalent paths (explicit edge vs XYZ hub) agree', () => {
  const withEdge = standardGraph();
  withEdge.addEdge({
    from: 'srgb',
    to: 'display-p3',
    matrix: withEdge.hubMatrix('srgb', 'display-p3'),
    label: 'direct srgb->p3',
  });
  const graphA = withEdge.compile();
  const graphB = standardCompiledGraph();
  const viaEdge = new Pipeline(graphA, { source: 'srgb', target: 'display-p3', strategy: 'preserve' });
  const viaHub = new Pipeline(graphB, { source: 'srgb', target: 'display-p3', strategy: 'preserve' });
  assert.ok(viaEdge.steps.some((s) => s.detail.includes('direct srgb->p3')));
  for (const color of REFERENCE_COLORS) {
    const a = viaEdge.convertPixel(...color).rgb;
    const b = viaHub.convertPixel(...color).rgb;
    assert.ok(approxVec3(a, b, 1e-12), `paths disagree on ${color}: ${a} vs ${b}`);
  }
});

test('different white points: adaptation step is present and matches Bradford reference', () => {
  const graph = standardCompiledGraph();
  const pipeline = new Pipeline(graph, { source: 'srgb', target: 'prophoto', strategy: 'preserve' });
  const adaptSteps = pipeline.steps.filter((s) => s.kind === 'adapt');
  assert.equal(adaptSteps.length, 1, 'adaptation must not be omitted for D65 -> D50');
  assert.match(adaptSteps[0].detail, /D65 -> D50/);

  // Reference: white -> XYZ(D65) -> Bradford adapt to D50 -> prophoto linear -> gamma encode.
  const whiteXyz = mat3Apply(rgbToXyzMatrix(srgbSpace()), [1, 1, 1]);
  const adapted = mat3Apply(adaptationMatrix(D65, D50), whiteXyz);
  const expectedWhiteXyz = whiteToXYZ(D50);
  assert.ok(approxVec3(adapted, expectedWhiteXyz, 1e-9), 'adapted white must land on D50');

  const converted = pipeline.convertPixel(1, 1, 1).rgb;
  assert.ok(approxVec3(converted, [1, 1, 1], 1e-9), 'white must map to white through adaptation');

  // If adaptation had been (illegally) skipped, white would land far off.
  const skipped = mat3Apply(rgbToXyzMatrix(srgbSpace()), [1, 1, 1]);
  assert.ok(
    !approxVec3(skipped, expectedWhiteXyz, 1e-3),
    'test premise broken: unadapted white unexpectedly matches D50',
  );
});

test('prophoto white round-trips back to srgb white through adaptation', () => {
  const graph = standardCompiledGraph();
  const forward = new Pipeline(graph, { source: 'prophoto', target: 'srgb', strategy: 'preserve' });
  const backward = new Pipeline(graph, { source: 'srgb', target: 'prophoto', strategy: 'preserve' });
  const there = forward.convertPixel(1, 1, 1).rgb;
  assert.ok(approxVec3(there, [1, 1, 1], TOLERANCE), `white in srgb: ${there}`);
  const back = backward.convertPixel(...there).rgb;
  assert.ok(approxVec3(back, [1, 1, 1], TOLERANCE), `white round trip: ${back}`);
});

test('prophoto space definition sanity', () => {
  const def = prophotoSpace();
  assert.equal(def.white.name, 'D50');
  assert.equal(def.transfer.id, 'gamma-1.8');
});
