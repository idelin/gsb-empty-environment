import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  AdaptationRequiredError,
  CycleContradictionError,
  D50,
  D65,
  SRGB_TO_XYZ,
  SingularMatrixError,
  compileGraph,
  linearSrgbSpace,
  srgbSpace,
  xyzSpace,
} from '../src/index.ts';
import type { GraphConfig, Mat3, Vec3 } from '../src/index.ts';

function baseConfig(): GraphConfig {
  return {
    spaces: [srgbSpace(), linearSrgbSpace(), xyzSpace(D65, 'xyz-d65'), xyzSpace(D50, 'xyz-d50')],
    edges: [
      { from: 'srgb', to: 'linear-srgb' },
      { from: 'linear-srgb', to: 'xyz-d65' },
      { from: 'srgb', to: 'xyz-d65' },
      { from: 'xyz-d65', to: 'xyz-d50' },
    ],
  };
}

test('two theoretically equivalent paths compile and agree numerically', () => {
  const plan = compileGraph(baseConfig());
  const probes: Vec3[] = [[0, 0, 0], [1, 1, 1], [1, 0, 0], [0.25, 0.5, 0.75], [0.9, 0.1, 0.3]];
  for (const p of probes) {
    const direct = plan.convert('srgb', 'xyz-d65', p);
    const viaLinear = plan.convert('linear-srgb', 'xyz-d65', plan.convert('srgb', 'linear-srgb', p));
    for (let c = 0; c < 3; c++) {
      assert.ok(Math.abs(direct[c] - viaLinear[c]) < 1e-9, `probe ${p} ch${c}`);
    }
  }
});

test('contradictory cycle is rejected at compile time', () => {
  const config = baseConfig();
  const skewed = SRGB_TO_XYZ.map((v) => v * 1.05) as Mat3;
  config.edges.push({ from: 'srgb', to: 'xyz-d65', kind: 'matrix', matrix: skewed, label: 'skewed' });
  assert.throws(() => compileGraph(config), CycleContradictionError);
});

test('singular matrix edge is rejected at compile time', () => {
  const config = baseConfig();
  const singular: Mat3 = [1, 2, 3, 2, 4, 6, 1, 1, 1];
  config.edges.push({ from: 'srgb', to: 'linear-srgb', kind: 'matrix', matrix: singular });
  assert.throws(() => compileGraph(config), SingularMatrixError);
});

test('space with singular rgbToXYZ is rejected at compile time', () => {
  const config = baseConfig();
  config.spaces.push({
    id: 'broken',
    white: [...D65],
    transfer: srgbSpace().transfer,
    rgbToXYZ: [0, 0, 0, 0, 0, 0, 0, 0, 0],
  });
  assert.throws(() => compileGraph(config), SingularMatrixError);
});

test('different white points get Bradford adaptation, never skipped', () => {
  const plan = compileGraph(baseConfig());
  const adapted = plan.convert('xyz-d65', 'xyz-d50', [D65[0], D65[1], D65[2]]);
  for (let c = 0; c < 3; c++) {
    assert.ok(Math.abs(adapted[c] - D50[c]) < 2e-3, `D65 white -> D50 white ch${c}: ${adapted[c]}`);
  }
  // adaptation:'none' with differing whites must fail at compile time
  const bad = baseConfig();
  bad.edges[3] = { from: 'xyz-d65', to: 'xyz-d50', adaptation: 'none' };
  assert.throws(() => compileGraph(bad), AdaptationRequiredError);
});

test('same white point compiles fine with adaptation:none', () => {
  const config: GraphConfig = {
    spaces: [srgbSpace(), linearSrgbSpace()],
    edges: [{ from: 'srgb', to: 'linear-srgb', adaptation: 'none' }],
  };
  assert.doesNotThrow(() => compileGraph(config));
});
