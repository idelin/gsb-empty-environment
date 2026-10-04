import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  latLngToCell,
  cellToLatLng,
  cellToParent,
  cellToChildren,
  getResolution,
  isValidCell,
  cellToFaceIJ,
  faceCenterCell,
  MAX_RESOLUTION,
} from '../src/cell.ts';

const BEIJING = { lat: 39.9042, lng: 116.4074 };

test('acceptance: GPS coordinate encodes to a res-8 hex string', () => {
  const cell = latLngToCell(BEIJING.lat, BEIJING.lng, 8);
  assert.equal(typeof cell, 'string');
  assert.match(cell, /^[0-9a-f]{16}$/);
  assert.equal(getResolution(cell), 8);
  assert.ok(isValidCell(cell));
});

test('encoding is deterministic and stable', () => {
  const a = latLngToCell(BEIJING.lat, BEIJING.lng, 8);
  const b = latLngToCell(BEIJING.lat, BEIJING.lng, 8);
  assert.equal(a, b);
  // golden value: guards against accidental geometry changes
  assert.equal(a, '18228614efffffff');
});

test('cell centre decodes back to the same cell (roundtrip)', () => {
  for (const res of [0, 1, 3, 8, 12]) {
    const cell = latLngToCell(BEIJING.lat, BEIJING.lng, res);
    const center = cellToLatLng(cell);
    assert.equal(latLngToCell(center.lat, center.lng, res), cell);
  }
});

test('cell centre is geographically close to the encoded point', () => {
  const cell = latLngToCell(BEIJING.lat, BEIJING.lng, 8);
  const center = cellToLatLng(cell);
  const dLat = Math.abs(center.lat - BEIJING.lat);
  const dLng = Math.abs(center.lng - BEIJING.lng);
  assert.ok(dLat < 0.05 && dLng < 0.05, `centre too far: ${dLat}, ${dLng}`);
});

test('full-sphere sweep: every coordinate encodes to a valid cell', () => {
  for (let lat = -85; lat <= 85; lat += 7.5) {
    for (let lng = -180; lng < 180; lng += 7.5) {
      const cell = latLngToCell(lat, lng, 8);
      assert.ok(isValidCell(cell), `invalid cell for ${lat},${lng}`);
    }
  }
});

test('parent/child hierarchy: 7 children, containment, zoom chain', () => {
  const cell = latLngToCell(BEIJING.lat, BEIJING.lng, 8);
  const parent = cellToParent(cell);
  assert.equal(getResolution(parent), 7);
  const children = cellToChildren(parent);
  assert.equal(children.length, 7);
  assert.ok(children.includes(cell));
  for (const child of children) {
    assert.equal(cellToParent(child), parent);
  }
  // zoom to any ancestor level
  const res2 = cellToParent(cell, 2);
  assert.equal(getResolution(res2), 2);
  // grandchildren at res+2: 49
  const grand = cellToChildren(cellToParent(cell, 6), 8);
  assert.equal(grand.length, 49);
  assert.ok(grand.includes(cell));
});

test('children IJ positions are the centre plus the 6 directions', () => {
  const parent = faceCenterCell(3, 4);
  const kids = cellToChildren(parent).map((c) => cellToFaceIJ(c).ij);
  const keys = kids.map((k) => `${k.i},${k.j}`).sort();
  assert.equal(new Set(keys).size, 7);
});

test('invalid cells are rejected', () => {
  assert.ok(!isValidCell(''));
  assert.ok(!isValidCell('xyz'));
  assert.ok(!isValidCell('18228614efffff')); // too short
  assert.ok(!isValidCell('28228614efffffff')); // bad mode
  assert.ok(!isValidCell('18228614efffff7f')); // used digit slot = 7
  assert.ok(!isValidCell('1ff28614efffffff')); // face 31 out of range
  assert.ok(!isValidCell('18228614effffeff')); // unused slot != 7
});

test('invalid coordinates and resolutions are rejected', () => {
  assert.throws(() => latLngToCell(91, 0, 8));
  assert.throws(() => latLngToCell(0, 181, 8));
  assert.throws(() => latLngToCell(NaN, 0, 8));
  assert.throws(() => latLngToCell(0, 0, -1));
  assert.throws(() => latLngToCell(0, 0, MAX_RESOLUTION + 1));
  assert.throws(() => cellToParent(faceCenterCell(0, 0)));
  assert.throws(() => cellToChildren(faceCenterCell(0, MAX_RESOLUTION)));
});
