import { test } from 'node:test';
import assert from 'node:assert/strict';
import { vec3 } from '../src/vec3.ts';
import type { Entity, WarningBeacon } from '../src/engine.ts';
import { CollisionWarningEngine } from '../src/engine.ts';

const CONFIG = {
  cellSize: 200,
  slotDuration: 1,
  horizon: 10,
  minSeparation: 50,
  warningLeadTime: 5,
};

/** Head-on pair: center distance hits 60 m (50 m + 2x5 m radii) at t=10 s. */
const headOnPair = (): [Entity, Entity] => [
  { id: 'A', position: vec3(0, 8000, 3000), velocity: vec3(100, 0, 0), radius: 5 },
  { id: 'B', position: vec3(2060, 8000, 3000), velocity: vec3(-100, 0, 0), radius: 5 },
];

test('breach is predicted within the 10 s horizon but not yet warned at t=0', () => {
  const engine = new CollisionWarningEngine(CONFIG);
  for (const e of headOnPair()) engine.addEntity(e);
  const beacons = engine.scan();
  assert.equal(beacons.length, 0); // breach is 10 s out, beyond the 5 s lead
});

test('beacon fires exactly 5 s before the predicted breach', () => {
  const engine = new CollisionWarningEngine(CONFIG);
  for (const e of headOnPair()) engine.addEntity(e);

  let beacon: WarningBeacon | undefined;
  for (let step = 0; step <= 5 && beacon === undefined; step++) {
    const found = engine.scan();
    if (found.length > 0) beacon = found[0];
    if (step < 5) engine.advance(1);
  }

  assert.ok(beacon, 'expected a beacon by t=5');
  assert.equal(beacon.entityA, 'A');
  assert.equal(beacon.entityB, 'B');
  assert.equal(beacon.issuedAt, 5);
  assert.ok(Math.abs(beacon.timeToBreach - 5) < 1e-9);
  assert.ok(Math.abs(beacon.predictedBreachAt - 10) < 1e-9);
  // 1060 m center distance minus two 5 m radii.
  assert.ok(Math.abs(beacon.currentDistance - 1050) < 1e-9);
  assert.equal(beacon.minSeparation, 50);
});

test('beacon is emitted once and stays active', () => {
  const engine = new CollisionWarningEngine(CONFIG);
  for (const e of headOnPair()) engine.addEntity(e);

  let total = 0;
  for (let step = 0; step <= 8; step++) {
    total += engine.scan().length;
    engine.advance(1);
  }
  assert.equal(total, 1);
  assert.equal(engine.activeWarnings().length, 1);
});

test('no false positives for well-separated parallel traffic', () => {
  const engine = new CollisionWarningEngine(CONFIG);
  engine.addEntity({
    id: 'P1',
    position: vec3(0, 0, 0),
    velocity: vec3(100, 0, 0),
    radius: 5,
  });
  engine.addEntity({
    id: 'P2',
    position: vec3(0, 500, 0),
    velocity: vec3(100, 0, 0),
    radius: 5,
  });
  for (let step = 0; step <= 10; step++) {
    assert.equal(engine.scan().length, 0);
    engine.advance(1);
  }
});

test('beacon retires when the conflict resolves', () => {
  const engine = new CollisionWarningEngine(CONFIG);
  const [a, b] = headOnPair();
  engine.addEntity(a);
  engine.addEntity(b);
  for (let step = 0; step < 5; step++) engine.advance(1);
  assert.equal(engine.scan().length, 1);
  // B climbs away: conflict disappears, warning must retire.
  b.velocity = vec3(-100, 0, 200);
  assert.equal(engine.scan().length, 0);
  assert.equal(engine.activeWarnings().length, 0);
});

test('scales to 500 entities with the full 10 s horizon', () => {
  const engine = new CollisionWarningEngine(CONFIG);
  let seed = 42;
  const rand = () => {
    seed = (seed * 1103515245 + 12345) % 2147483648;
    return seed / 2147483648;
  };
  for (let i = 0; i < 500; i++) {
    engine.addEntity({
      id: `T${i}`,
      position: vec3(rand() * 40000 - 20000, rand() * 40000 - 20000, rand() * 4000),
      velocity: vec3(rand() * 200 - 100, rand() * 200 - 100, rand() * 20 - 10),
      radius: 5,
    });
  }
  assert.equal(engine.entityCount, 500);
  const start = performance.now();
  engine.scan();
  const elapsed = performance.now() - start;
  assert.ok(elapsed < 1000, `scan too slow: ${elapsed.toFixed(1)} ms`);
});
