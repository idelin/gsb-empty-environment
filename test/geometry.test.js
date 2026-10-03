'use strict';
const assert = require('assert/strict');
const { test, describe } = require('./compat');
const {
  vec3,
  distance,
  segmentSegmentDistance,
  capsuleSurfaceGap,
  firstContactTime,
} = require('../dist');

describe('geometry', () => {
  test('distance between parallel segments', () => {
    const d = segmentSegmentDistance(vec3(0, 0, 0), vec3(2, 0, 0), vec3(0, 1, 0), vec3(2, 1, 0));
    assert.ok(Math.abs(d - 1) < 1e-9, `expected 1, got ${d}`);
  });

  test('crossing segments have zero distance', () => {
    const d = segmentSegmentDistance(vec3(-1, 0, 0), vec3(1, 0, 0), vec3(0, -1, 0), vec3(0, 1, 0));
    assert.ok(Math.abs(d) < 1e-9, `expected 0, got ${d}`);
  });

  test('skew segments closest points', () => {
    const d = segmentSegmentDistance(vec3(0, 0, 0), vec3(2, 0, 0), vec3(1, 1, 1), vec3(1, 1, -1));
    assert.ok(Math.abs(d - 1) < 1e-9, `expected 1, got ${d}`);
  });

  test('degenerate point-to-segment distance', () => {
    const d = segmentSegmentDistance(vec3(0, 5, 0), vec3(0, 5, 0), vec3(-3, 0, 0), vec3(3, 0, 0));
    assert.ok(Math.abs(d - 5) < 1e-9, `expected 5, got ${d}`);
  });

  test('capsule surface gap subtracts both radii', () => {
    const a = { start: vec3(0, 0, 0), end: vec3(10, 0, 0), radius: 2 };
    const b = { start: vec3(0, 10, 0), end: vec3(10, 10, 0), radius: 3 };
    const gap = capsuleSurfaceGap(a, b);
    assert.ok(Math.abs(gap - 5) < 1e-9, `expected 5, got ${gap}`);
  });

  test('firstContactTime solves head-on approach', () => {
    // relPos -120 on x, closing at 100 m/s, threshold 70 -> contact at tau 0.5
    const tau = firstContactTime(vec3(-120, 0, 0), vec3(100, 0, 0), 70, 1);
    assert.ok(tau !== null && Math.abs(tau - 0.5) < 1e-9, `expected 0.5, got ${tau}`);
  });

  test('firstContactTime returns 0 when already inside threshold', () => {
    const tau = firstContactTime(vec3(10, 0, 0), vec3(0, 0, 0), 70, 1);
    assert.equal(tau, 0);
  });

  test('firstContactTime returns null when trajectories never meet', () => {
    const tau = firstContactTime(vec3(0, 500, 0), vec3(100, 0, 0), 70, 1);
    assert.equal(tau, null);
  });

  test('distance helper', () => {
    assert.equal(distance(vec3(0, 0, 0), vec3(3, 4, 0)), 5);
  });
});
