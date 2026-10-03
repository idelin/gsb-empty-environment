'use strict';
const assert = require('assert/strict');
const { test, describe } = require('./compat');
const { SpatioTemporalGrid, vec3 } = require('../dist');

describe('SpatioTemporalGrid', () => {
  test('entities sharing a spatio-temporal cell find each other', () => {
    const grid = new SpatioTemporalGrid(100);
    const capA = { start: vec3(0, 0, 0), end: vec3(50, 0, 0), radius: 10 };
    const capB = { start: vec3(60, 5, 0), end: vec3(90, 5, 0), radius: 10 };
    grid.insert(0, 1, capA);
    grid.insert(1, 1, capB);
    assert.ok(grid.query(0, 1, capA).has(1));
    assert.ok(grid.query(1, 1, capB).has(0));
  });

  test('time slots isolate entities at the same location', () => {
    const grid = new SpatioTemporalGrid(100);
    const cap = { start: vec3(0, 0, 0), end: vec3(50, 0, 0), radius: 10 };
    grid.insert(0, 1, cap);
    grid.insert(1, 2, cap);
    assert.equal(grid.query(0, 1, cap).size, 0);
    assert.equal(grid.query(1, 2, cap).size, 0);
  });

  test('distant entities in the same slot are not candidates', () => {
    const grid = new SpatioTemporalGrid(100);
    const capA = { start: vec3(0, 0, 0), end: vec3(10, 0, 0), radius: 5 };
    const capB = { start: vec3(10000, 0, 0), end: vec3(10010, 0, 0), radius: 5 };
    grid.insert(0, 1, capA);
    grid.insert(1, 1, capB);
    assert.equal(grid.query(0, 1, capA).size, 0);
  });

  test('query excludes the querying entity itself', () => {
    const grid = new SpatioTemporalGrid(100);
    const cap = { start: vec3(0, 0, 0), end: vec3(10, 0, 0), radius: 5 };
    grid.insert(7, 3, cap);
    assert.equal(grid.query(7, 3, cap).size, 0);
  });

  test('clear resets cells and capsules', () => {
    const grid = new SpatioTemporalGrid(100);
    const cap = { start: vec3(0, 0, 0), end: vec3(10, 0, 0), radius: 5 };
    grid.insert(0, 1, cap);
    assert.ok(grid.cellCount > 0);
    grid.clear();
    assert.equal(grid.cellCount, 0);
    assert.equal(grid.getCapsule(0, 1), undefined);
  });

  test('capsule is retrievable per entity and slot', () => {
    const grid = new SpatioTemporalGrid(100);
    const cap = { start: vec3(1, 2, 3), end: vec3(4, 5, 6), radius: 7 };
    grid.insert(3, 4, cap);
    assert.deepEqual(grid.getCapsule(3, 4), cap);
  });
});
