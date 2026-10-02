import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TileGrid } from '../src/grid.ts';

const spec = { minLat: 39.8, minLng: 116.2, maxLat: 40.1, maxLng: 116.6, tileSizeMeters: 500 };

test('grid dimensions cover the bbox', () => {
  const g = new TileGrid(spec);
  assert.ok(g.rows > 0 && g.cols > 0);
  assert.equal(g.cellCount, g.rows * g.cols);
  // ~33.4km x ~34.1km at 500m tiles
  assert.ok(g.rows >= 66 && g.rows <= 68);
  assert.ok(g.cols >= 67 && g.cols <= 70);
});

test('cellOf buckets corners and rejects outside points', () => {
  const g = new TileGrid(spec);
  assert.equal(g.cellOf(39.8, 116.2), 0); // min corner -> cell 0
  assert.equal(g.cellOf(40.1 - 1e-9, 116.6 - 1e-9), g.cellCount - 1);
  assert.equal(g.cellOf(39.7, 116.4), -1);
  assert.equal(g.cellOf(40.1, 116.4), -1); // max edge is exclusive
  assert.equal(g.cellOf(39.9, 116.1), -1);
  assert.equal(g.cellOf(39.9, 116.6), -1);
});

test('centerOf round-trips through cellOf', () => {
  const g = new TileGrid(spec);
  for (const cell of [0, 1, 123, g.cellCount - 1, Math.floor(g.cellCount / 2)]) {
    const { lat, lng } = g.centerOf(cell);
    assert.equal(g.cellOf(lat, lng), cell);
  }
});

test('adjacent cells are distinct', () => {
  const g = new TileGrid(spec);
  const a = g.cellOf(39.9, 116.4);
  const b = g.cellOf(39.9, 116.4 + 0.006); // ~500m east
  assert.notEqual(a, b);
});

test('invalid specs are rejected', () => {
  assert.throws(() => new TileGrid({ ...spec, maxLat: 39.8 }));
  assert.throws(() => new TileGrid({ ...spec, tileSizeMeters: 0 }));
});
