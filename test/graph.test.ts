import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  ConversionGraph,
  CycleContradictionError,
  PathContradictionError,
  SingularMatrixError,
  mat3Invert,
  mat3Multiply,
  rgbToXyzMatrix,
  xyzToRgbMatrix,
  srgbSpace,
  srgbLinearSpace,
  displayP3Space,
  prophotoSpace,
  xyzSpace,
  type ColorSpaceDef,
} from '../src/index.ts';
import { standardGraph } from './helpers.ts';
import { linearTransfer, D65 } from '../src/index.ts';

test('compile rejects a singular explicit edge matrix', () => {
  const graph = standardGraph();
  const singular: [number, number, number, number, number, number, number, number, number] = [
    1, 2, 3,
    2, 4, 6,
    0, 1, 0,
  ];
  assert.throws(
    () => graph.addEdge({ from: 'srgb', to: 'display-p3', matrix: singular }),
    SingularMatrixError,
  );
});

test('compile rejects degenerate (collinear) primaries as singular', () => {
  const graph = new ConversionGraph();
  const broken: ColorSpaceDef = {
    id: 'broken',
    kind: 'rgb',
    primaries: { r: [0.3, 0.3], g: [0.3, 0.3], b: [0.15, 0.06] },
    white: D65,
    transfer: linearTransfer,
  };
  graph.addSpace(broken);
  graph.addSpace(xyzSpace());
  assert.throws(() => graph.compile(), SingularMatrixError);
});

test('consistent cycle of explicit edges compiles', () => {
  const graph = standardGraph();
  const forward = graph.hubMatrix('srgb', 'display-p3');
  graph.addEdge({ from: 'srgb', to: 'display-p3', matrix: forward });
  graph.addEdge({ from: 'display-p3', to: 'srgb', matrix: mat3Invert(forward) });
  const compiled = graph.compile();
  assert.equal(compiled.pathChecks.length, 2);
  for (const check of compiled.pathChecks) {
    assert.ok(check.maxDeviation <= 1e-6, `deviation ${check.maxDeviation}`);
  }
});

test('contradictory cycle is rejected at compile time', () => {
  const graph = standardGraph();
  const forward = graph.hubMatrix('srgb', 'display-p3');
  graph.addEdge({ from: 'srgb', to: 'display-p3', matrix: forward });
  // Return edge is NOT the inverse: the cycle composes to a non-identity.
  const wrongBack = mat3Invert(forward).map((v) => v * 1.5) as typeof forward;
  graph.addEdge({ from: 'display-p3', to: 'srgb', matrix: wrongBack });
  assert.throws(() => graph.compile(), CycleContradictionError);
});

test('explicit edge must agree with the derived hub path', () => {
  const graph = standardGraph();
  const composed = graph.hubMatrix('srgb', 'display-p3');
  graph.addEdge({ from: 'srgb', to: 'display-p3', matrix: composed });
  const compiled = graph.compile();
  assert.ok(compiled.pathChecks[0].maxDeviation < 1e-12);
});

test('edge that silently drops white-point adaptation is a contradiction', () => {
  const graph = standardGraph();
  // srgb (D65) -> prophoto (D50) REQUIRES Bradford adaptation; an edge
  // that only composes the primary matrices must be rejected.
  const withoutAdaptation = mat3Multiply(
    xyzToRgbMatrix(prophotoSpace()),
    rgbToXyzMatrix(srgbSpace()),
  );
  graph.addEdge({ from: 'srgb', to: 'prophoto', matrix: withoutAdaptation });
  assert.throws(() => graph.compile(), PathContradictionError);
});

test('hub matrix requires adaptation exactly when white points differ', () => {
  const compiled = standardGraph().compile();
  assert.equal(compiled.requiresAdaptation('srgb', 'display-p3'), false);
  assert.equal(compiled.requiresAdaptation('srgb', 'prophoto'), true);
});

test('unknown spaces are rejected', () => {
  const graph = new ConversionGraph();
  graph.addSpace(srgbSpace());
  assert.throws(() => graph.addEdge({ from: 'srgb', to: 'nope', matrix: [1, 0, 0, 0, 1, 0, 0, 0, 1] }));
  assert.throws(() => graph.hubMatrix('srgb', 'nope'));
});

test('all builtin spaces compile and invert cleanly', () => {
  const graph = new ConversionGraph();
  for (const space of [srgbSpace(), srgbLinearSpace(), displayP3Space(), prophotoSpace(), xyzSpace()]) {
    graph.addSpace(space);
  }
  assert.doesNotThrow(() => graph.compile());
});
