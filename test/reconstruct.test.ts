import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  StructuringElement,
  makePlane,
  planeFromAscii,
  planeFromChannel,
  planeFromLuminance,
  planeToAscii,
  reconstructReference,
  rgbaFromAscii,
  symmetricDeltas,
  type Plane,
} from '../src/index.ts';

function randomPlane(width: number, height: number, density: number, seed: number): Plane {
  let s = seed >>> 0;
  const rnd = () => ((s = (s * 1664525 + 1013904223) >>> 0) / 2 ** 32);
  const plane = makePlane(width, height);
  for (let i = 0; i < plane.data.length; i++) plane.data[i] = rnd() < density ? 1 : 0;
  return plane;
}

/** Independent reference: reconstruction = mask components touching the marker. */
function componentsReference(marker: Plane, mask: Plane, se: StructuringElement): Plane {
  const { width, height } = mask;
  const out = makePlane(width, height);
  const deltas = symmetricDeltas(se);
  const seen = new Uint8Array(width * height);
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      const i = y * width + x;
      if (mask.data[i] === 0 || seen[i]) continue;
      // flood one mask component
      const comp: number[] = [];
      let touchesMarker = false;
      const stack = [i];
      seen[i] = 1;
      while (stack.length > 0) {
        const c = stack.pop() as number;
        comp.push(c);
        if (marker.data[c] > 0) touchesMarker = true;
        const cx = c % width;
        const cy = Math.floor(c / width);
        for (const [dx, dy] of deltas) {
          const nx = cx + dx;
          const ny = cy + dy;
          if (nx < 0 || ny < 0 || nx >= width || ny >= height) continue;
          const ni = ny * width + nx;
          if (!seen[ni] && mask.data[ni] > 0) {
            seen[ni] = 1;
            stack.push(ni);
          }
        }
      }
      if (touchesMarker) for (const c of comp) out.data[c] = 1;
    }
  }
  return out;
}

test('reconstruction stays within the mask and covers the marker', () => {
  const se = StructuringElement.square(1);
  for (let seed = 1; seed <= 15; seed++) {
    const mask = randomPlane(19, 13, 0.5, seed);
    const marker = randomPlane(19, 13, 0.15, seed * 7 + 1);
    const { result, converged } = reconstructReference(marker, mask, se);
    assert.ok(converged);
    for (let i = 0; i < result.data.length; i++) {
      assert.ok(result.data[i] <= (mask.data[i] > 0 ? 1 : 0), 'result exceeds mask');
      if (marker.data[i] > 0 && mask.data[i] > 0) assert.equal(result.data[i], 1, 'marker lost');
    }
  }
});

test('iterative fixed point equals the connected-components characterisation', () => {
  const ses = [StructuringElement.cross(1), StructuringElement.square(1)];
  for (const se of ses) {
    for (let seed = 1; seed <= 15; seed++) {
      const mask = randomPlane(16, 12, 0.55, seed * 3);
      const marker = randomPlane(16, 12, 0.2, seed * 5 + 2);
      const { result, converged } = reconstructReference(marker, mask, se);
      assert.ok(converged);
      const expected = componentsReference(marker, mask, se);
      assert.deepEqual([...result.data], [...expected.data]);
    }
  }
});

test('propagation is not truncated by the kernel radius', () => {
  // 1xN corridor: a 3x1 SE has reach 1, but the fixed point must travel the
  // whole corridor (here 60 px).
  const width = 64;
  const mask = makePlane(width, 1);
  mask.data.fill(1, 0, 60);
  const marker = makePlane(width, 1);
  marker.data[0] = 1;
  const se = new StructuringElement([[0, 0], [1, 0]], [0, 0]);
  const { result, converged, iterations } = reconstructReference(marker, mask, se);
  assert.ok(converged);
  assert.ok(iterations > 50, 'fixed point needs many waves');
  assert.equal(result.data[59], 1);
  assert.equal(result.data[60], 0);
});

test('budget exhaustion is reported, not silently truncated', () => {
  const mask = planeFromAscii(['##########']);
  const marker = planeFromAscii(['#.........']);
  const se = new StructuringElement([[0, 0], [1, 0]], [0, 0]);
  const r = reconstructReference(marker, mask, se, 'zero', 3);
  assert.equal(r.converged, false);
  assert.equal(r.iterations, 3);
  const full = reconstructReference(marker, mask, se);
  assert.equal(full.converged, true);
  assert.deepEqual(planeToAscii(full.result), ['##########']);
});

test('RGBA workflow: build image, threshold a channel, reconstruct', () => {
  const palette = {
    '.': [0, 0, 0, 255], // outside: dark
    M: [255, 255, 255, 255], // mask border: bright
    g: [200, 200, 200, 255], // mask interior: grey
    S: [255, 120, 120, 255], // seed: red, bright enough to stay inside the mask
    T: [0, 0, 0, 0], // transparent hole in the mask
  } as const;
  const img = rgbaFromAscii(
    [
      'MMMMMMMMMMM',
      'MggggTggggM',
      'MggSggggggM',
      'MgggggTgggM',
      'MMMMMMMMMMM',
    ],
    palette,
  );
  const mask = planeFromLuminance(img, 128);
  const red = planeFromChannel(img, 0, 128);
  const green = planeFromChannel(img, 1, 128);
  const marker = makePlane(img.width, img.height);
  for (let i = 0; i < marker.data.length; i++) marker.data[i] = red.data[i] && !green.data[i] ? 1 : 0;
  assert.equal(marker.data[2 * 11 + 3], 1, 'seed extracted from the red channel');
  const { result, converged } = reconstructReference(marker, mask, StructuringElement.cross(1));
  assert.ok(converged);
  // the seed floods the whole mask but cannot cross the transparent holes
  assert.equal(result.data[1 * 11 + 1], 1);
  assert.equal(result.data[3 * 11 + 9], 1);
  assert.equal(result.data[1 * 11 + 5], 0, 'hole must stay background');
  assert.equal(result.data[3 * 11 + 6], 0, 'hole must stay background');
  for (let i = 0; i < result.data.length; i++) assert.ok(result.data[i] <= mask.data[i]);
});
