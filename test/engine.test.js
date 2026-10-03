'use strict';
const assert = require('assert/strict');
const { test, describe } = require('./compat');
const { CollisionWarningEngine } = require('../dist');

function headOnPair() {
  // Center distance closes at 100 m/s; surface gap hits 50 m
  // (center distance 70 m) at t = (1020 - 70) / 100 = 9.5 s.
  return [
    { id: 'A', position: { x: 0, y: 0, z: 1000 }, velocity: { x: 50, y: 0, z: 0 }, radius: 10 },
    { id: 'B', position: { x: 1020, y: 0, z: 1000 }, velocity: { x: -50, y: 0, z: 0 }, radius: 10 },
  ];
}

function makeEngine() {
  return new CollisionWarningEngine({
    minSeparation: 50,
    slotDuration: 1,
    horizonSlots: 10,
    cellSize: 256,
  });
}

describe('CollisionWarningEngine', () => {
  test('predicts the 50 m breach 9.5 s ahead with current topology distance', () => {
    const engine = makeEngine();
    const beacons = engine.evaluate(headOnPair(), 0);
    assert.equal(beacons.length, 1);
    const b = beacons[0];
    assert.equal(b.entityA, 'A');
    assert.equal(b.entityB, 'B');
    assert.equal(b.issuedAt, 0);
    assert.ok(Math.abs(b.predictedBreachTime - 9.5) < 1e-9,
      `predictedBreachTime ${b.predictedBreachTime}`);
    assert.ok(Math.abs(b.timeToBreach - 9.5) < 1e-9, `timeToBreach ${b.timeToBreach}`);
    assert.ok(Math.abs(b.currentDistance - 1020) < 1e-9,
      `currentDistance ${b.currentDistance}`);
    assert.ok(Math.abs(b.surfaceGap - 1000) < 1e-9, `surfaceGap ${b.surfaceGap}`);
    assert.equal(b.minSeparation, 50);
    assert.equal(b.slot, 10);
  });

  test('warning is raised at least 5 s before the breach', () => {
    const engine = makeEngine();
    const [b] = engine.evaluate(headOnPair(), 0);
    assert.ok(b.timeToBreach >= 5, `lead time ${b.timeToBreach}s must be >= 5s`);
  });

  test('each pair is alerted only once (dedup across ticks)', () => {
    const engine = makeEngine();
    const entities = headOnPair();
    assert.equal(engine.evaluate(entities, 0).length, 1);
    // advance 2 s and re-evaluate: no duplicate beacon
    for (const e of entities) {
      e.position.x += e.velocity.x * 2;
    }
    assert.equal(engine.evaluate(entities, 2).length, 0);
    assert.equal(engine.getActiveAlerts().length, 1);
  });

  test('no alert for traffic keeping safe separation', () => {
    const engine = makeEngine();
    const entities = [
      { id: 'P', position: { x: 0, y: 0, z: 0 }, velocity: { x: 100, y: 0, z: 0 }, radius: 10 },
      { id: 'Q', position: { x: 0, y: 500, z: 0 }, velocity: { x: 100, y: 0, z: 0 }, radius: 10 },
    ];
    assert.equal(engine.evaluate(entities, 0).length, 0);
  });

  test('no alert when breach lies beyond the horizon', () => {
    const engine = makeEngine();
    const entities = [
      { id: 'A', position: { x: 0, y: 0, z: 0 }, velocity: { x: 50, y: 0, z: 0 }, radius: 10 },
      { id: 'B', position: { x: 5000, y: 0, z: 0 }, velocity: { x: -50, y: 0, z: 0 }, radius: 10 },
    ];
    // breach at t = (5000 - 70) / 100 = 49.3 s > 10 s horizon
    assert.equal(engine.evaluate(entities, 0).length, 0);
  });

  test('alert for already-breaching pair reports zero time to breach', () => {
    const engine = makeEngine();
    const entities = [
      { id: 'A', position: { x: 0, y: 0, z: 0 }, velocity: { x: 0, y: 0, z: 0 }, radius: 10 },
      { id: 'B', position: { x: 50, y: 0, z: 0 }, velocity: { x: 0, y: 0, z: 0 }, radius: 10 },
    ];
    const [b] = engine.evaluate(entities, 3);
    assert.equal(b.timeToBreach, 0);
    assert.equal(b.predictedBreachTime, 3);
  });

  test('broad phase prunes the vast majority of pairs', () => {
    const engine = makeEngine();
    const entities = [];
    for (let i = 0; i < 500; i++) {
      entities.push({
        id: 'E' + i,
        position: { x: (i % 25) * 1000, y: Math.floor(i / 25) * 1000, z: 1000 },
        velocity: { x: 10, y: 0, z: 0 },
        radius: 10,
      });
    }
    engine.evaluate(entities, 0);
    const bruteForcePairs = (500 * 499) / 2;
    assert.ok(engine.stats.preciseChecks < bruteForcePairs / 100,
      `preciseChecks ${engine.stats.preciseChecks} should be << ${bruteForcePairs}`);
    assert.ok(engine.stats.cellsUsed > 0);
  });
});
