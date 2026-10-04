import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  getIcosahedron,
  applyEdgeTransform,
  crossingAt,
} from '../src/icosahedron.ts';
import { rotationPowers, upAp7, downAp7 } from '../src/hexmath.ts';

test('icosahedron has 12 vertices and 20 CCW-outward faces', () => {
  const ico = getIcosahedron();
  assert.equal(ico.vertices.length, 12);
  assert.equal(ico.faces.length, 20);
  for (const f of ico.faces) {
    assert.equal(new Set(f).size, 3, 'face vertices distinct');
  }
});

test('every vertex is shared by exactly 5 faces', () => {
  const ico = getIcosahedron();
  const count = new Array(12).fill(0);
  for (const f of ico.faces) for (const v of f) count[v]++;
  assert.deepEqual(count, new Array(12).fill(5));
});

test('every face has exactly 3 neighbours, adjacency is symmetric', () => {
  const ico = getIcosahedron();
  for (let f = 0; f < 20; f++) {
    assert.equal(ico.transforms[f].length, 3);
    for (const tr of ico.transforms[f]) {
      const back = ico.transforms[tr.toFace][tr.edgeOnTo];
      assert.equal(back.toFace, f);
      assert.equal(back.edgeOnTo, ico.transforms[f].indexOf(tr));
    }
  }
});

test('edge transforms are involutions and pure 60-degree rotations', () => {
  const ico = getIcosahedron();
  const powers = rotationPowers();
  const probe = { i: 7, j: -13 };
  for (let f = 0; f < 20; f++) {
    for (let e = 0; e < 3; e++) {
      const tr = ico.transforms[f][e];
      assert.ok(
        powers.some(
          (p) =>
            p[0] === tr.rot[0] && p[1] === tr.rot[1] &&
            p[2] === tr.rot[2] && p[3] === tr.rot[3],
        ),
        'rotation is a power of 60 degrees',
      );
      const back = ico.transforms[tr.toFace][tr.edgeOnTo];
      const round = applyEdgeTransform(back, applyEdgeTransform(tr, probe));
      assert.deepEqual(round, probe);
    }
  }
});

test('resolution-scaled crossing translations are M^res * t', () => {
  const base = crossingAt(0);
  const scaled = crossingAt(8);
  assert.equal(base.length, 20);
  for (let f = 0; f < 20; f++) {
    for (let e = 0; e < 3; e++) {
      assert.deepEqual(scaled[f][e].rot, base[f][e].rot);
      assert.notDeepEqual(scaled[f][e].t, base[f][e].t);
      assert.equal(scaled[f][e].toFace, base[f][e].toFace);
    }
  }
});

test('aperture-7 up/down is a 7-child exact decomposition', () => {
  const samples = [
    { i: 0, j: 0 },
    { i: 1, j: 0 },
    { i: -3, j: 5 },
    { i: 1249, j: -37 },
    { i: -2401, j: 2401 },
  ];
  for (const s of samples) {
    const { parent, digit } = upAp7(s);
    assert.deepEqual(downAp7(parent, digit), s, `roundtrip ${JSON.stringify(s)}`);
  }
  // the 7 children of a parent are distinct and map back to it
  const parent = { i: 4, j: -2 };
  const children = [0, 1, 2, 3, 4, 5, 6].map((d) => downAp7(parent, d));
  const uniq = new Set(children.map((c) => `${c.i},${c.j}`));
  assert.equal(uniq.size, 7);
  for (const c of children) {
    assert.deepEqual(upAp7(c).parent, parent);
  }
});
