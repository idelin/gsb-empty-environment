import { test } from 'node:test';
import assert from 'node:assert/strict';
import { vec3 } from '../src/vec3.ts';
import {
  makeCapsule,
  segmentSegmentDistance,
  capsuleDistance,
  timeOfSeparationBreach,
} from '../src/capsule.ts';

test('segment-segment distance: parallel segments', () => {
  const d = segmentSegmentDistance(
    vec3(0, 0, 0),
    vec3(10, 0, 0),
    vec3(0, 30, 0),
    vec3(10, 30, 0),
  );
  assert.equal(d, 30);
});

test('segment-segment distance: crossing segments touch', () => {
  const d = segmentSegmentDistance(
    vec3(-5, 0, 0),
    vec3(5, 0, 0),
    vec3(0, -5, 0),
    vec3(0, 5, 0),
  );
  assert.ok(Math.abs(d) < 1e-9);
});

test('segment-segment distance: skew segments', () => {
  const d = segmentSegmentDistance(
    vec3(0, 0, 0),
    vec3(10, 0, 0),
    vec3(5, 3, 4),
    vec3(5, 3, 14),
  );
  assert.ok(Math.abs(d - 5) < 1e-9);
});

test('capsule distance subtracts both radii', () => {
  const a = makeCapsule(vec3(0, 0, 0), vec3(10, 0, 0), 5);
  const b = makeCapsule(vec3(0, 30, 0), vec3(10, 30, 0), 5);
  assert.equal(capsuleDistance(a, b), 20);
});

test('capsule distance floors at zero when overlapping', () => {
  const a = makeCapsule(vec3(0, 0, 0), vec3(10, 0, 0), 5);
  const b = makeCapsule(vec3(0, 3, 0), vec3(10, 3, 0), 5);
  assert.equal(capsuleDistance(a, b), 0);
});

test('timeOfSeparationBreach: head-on closing at 200 m/s', () => {
  const t = timeOfSeparationBreach(
    vec3(0, 0, 0),
    vec3(100, 0, 0),
    vec3(2060, 0, 0),
    vec3(-100, 0, 0),
    60,
  );
  assert.equal(t, 10);
});

test('timeOfSeparationBreach: receding entities never breach', () => {
  const t = timeOfSeparationBreach(
    vec3(0, 0, 0),
    vec3(-100, 0, 0),
    vec3(2060, 0, 0),
    vec3(100, 0, 0),
    60,
  );
  assert.equal(t, undefined);
});

test('timeOfSeparationBreach: already inside separation returns start time', () => {
  const t = timeOfSeparationBreach(
    vec3(0, 0, 0),
    vec3(0, 0, 0),
    vec3(10, 0, 0),
    vec3(0, 0, 0),
    60,
    2,
  );
  assert.equal(t, 2);
});
