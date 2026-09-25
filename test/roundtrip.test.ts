import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  D65,
  compileGraph,
  linearSrgbSpace,
  makeTestImage,
  processBatch,
  srgbSpace,
  xyzSpace,
} from '../src/index.ts';
import type { GraphConfig, Vec3 } from '../src/index.ts';

function plan() {
  const config: GraphConfig = {
    spaces: [srgbSpace(), linearSrgbSpace(), xyzSpace(D65, 'xyz-d65')],
    edges: [
      { from: 'srgb', to: 'linear-srgb' },
      { from: 'linear-srgb', to: 'xyz-d65' },
      { from: 'srgb', to: 'xyz-d65' },
    ],
  };
  return compileGraph(config);
}

test('black, white and primaries roundtrip within tolerance', () => {
  const p = plan();
  const probes: Vec3[] = [
    [0, 0, 0],
    [1, 1, 1],
    [1, 0, 0],
    [0, 1, 0],
    [0, 0, 1],
    [0.5, 0.5, 0.5],
  ];
  for (const target of ['linear-srgb', 'xyz-d65']) {
    for (const probe of probes) {
      const there = p.convert('srgb', target, probe);
      const back = p.convert(target, 'srgb', there);
      for (let c = 0; c < 3; c++) {
        assert.ok(
          Math.abs(back[c] - probe[c]) < 1e-9,
          `srgb -> ${target} -> srgb failed for ${probe} ch${c}: ${back[c]}`,
        );
      }
    }
  }
});

test('white lands on the explicit white point', () => {
  const p = plan();
  const xyz = p.convert('srgb', 'xyz-d65', [1, 1, 1]);
  for (let c = 0; c < 3; c++) {
    assert.ok(Math.abs(xyz[c] - D65[c]) < 1e-3, `white ch${c}: ${xyz[c]} vs ${D65[c]}`);
  }
});

test('batch roundtrip error of the test image is within tolerance (preserve)', () => {
  const p = plan();
  const img = makeTestImage();
  const res = processBatch(img.data, p, 'srgb', 'linear-srgb', { strategy: 'preserve' });
  assert.ok(res.roundtrip.maxError < 1e-9, `maxError=${res.roundtrip.maxError}`);
  assert.ok(res.roundtrip.meanError < 1e-10, `meanError=${res.roundtrip.meanError}`);
  assert.equal(res.reversible, true);
});

test('negative linear components roundtrip exactly, no NaN anywhere', () => {
  const p = plan();
  // sRGB pixel with a negative (out-of-gamut) blue channel
  const px = Float64Array.from([0.4, 0.5, -0.3, 1]);
  const res = processBatch(px, p, 'srgb', 'linear-srgb', { strategy: 'preserve' });
  const linB = res.pixels[2];
  assert.ok(linB < 0 && Number.isFinite(linB), `linear blue ${linB}`);
  const back = processBatch(res.pixels, p, 'linear-srgb', 'srgb', { strategy: 'preserve' });
  assert.ok(Math.abs(back.pixels[2] - -0.3) < 1e-12);
  assert.ok(back.pixels.every((v) => Number.isFinite(v)));
});
