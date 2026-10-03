'use strict';
const assert = require('assert/strict');
const { test, describe } = require('./compat');
const { AirTrafficSimulation, distance } = require('../dist');

describe('AirTrafficSimulation acceptance (500 units)', () => {
  test('500 air-traffic units are simulated', () => {
    const sim = new AirTrafficSimulation();
    assert.equal(sim.units.length, 500);
  });

  test('engine warns about A/B 50 m breach >= 5 s before it happens', () => {
    const sim = new AirTrafficSimulation();
    const allBeacons = [];
    for (let s = 0; s < 12; s++) {
      allBeacons.push(...sim.step(1));
    }

    const beacon = allBeacons.find(
      (b) => (b.entityA === 'A' && b.entityB === 'B') || (b.entityA === 'B' && b.entityB === 'A'),
    );
    assert.ok(beacon, 'expected a warning beacon for pair A/B');

    // Issued at t=0, i.e. 9.5 s before the breach (>= the required 5 s lead).
    assert.equal(beacon.issuedAt, 0);
    assert.ok(beacon.timeToBreach >= 5, `lead time ${beacon.timeToBreach}s must be >= 5s`);

    // Ground truth: gap(t) = |1020 - 100 t| - 20 reaches 50 m at t = 9.5 s.
    const actualBreachTime = 9.5;
    assert.ok(
      Math.abs(beacon.predictedBreachTime - actualBreachTime) <= 0.5,
      `predicted ${beacon.predictedBreachTime}s vs actual ${actualBreachTime}s`,
    );

    // Beacon carries the pair's current topological distance at issue time.
    assert.ok(Math.abs(beacon.currentDistance - 1020) < 1e-9,
      `currentDistance ${beacon.currentDistance}`);
    assert.equal(beacon.minSeparation, 50);
  });

  test('beacon currentDistance matches the live topology distance', () => {
    const sim = new AirTrafficSimulation();
    const beacons = sim.step(1);
    const beacon = beacons.find((b) => b.entityA === 'A' && b.entityB === 'B');
    const a = sim.units.find((u) => u.id === 'A');
    const b = sim.units.find((u) => u.id === 'B');
    // beacon was issued at t=0, before positions advanced
    const distAtIssue = distance(
      { x: a.position.x - a.velocity.x, y: a.position.y, z: a.position.z },
      { x: b.position.x - b.velocity.x, y: b.position.y, z: b.position.z },
    );
    assert.ok(Math.abs(beacon.currentDistance - distAtIssue) < 1e-9);
  });

  test('prediction matches a fine-grained ground-truth integration', () => {
    const sim = new AirTrafficSimulation();
    const [beacon] = sim.step(1);

    // Fine-step the actual A/B trajectories at 1 ms resolution.
    const a = sim.units.find((u) => u.id === 'A');
    const b = sim.units.find((u) => u.id === 'B');
    let t = 0;
    let actual = null;
    const dt = 0.001;
    while (t <= 12) {
      const gap =
        Math.abs(1020 + b.velocity.x * t - a.velocity.x * t) - a.radius - b.radius;
      if (gap < 50) {
        actual = t;
        break;
      }
      t += dt;
    }
    assert.ok(actual !== null, 'breach should occur within 12 s');
    assert.ok(Math.abs(beacon.predictedBreachTime - actual) <= 0.5,
      `predicted ${beacon.predictedBreachTime}s vs integrated ${actual}s`);
  });

  test('engine stays fast and prunes at 500-unit scale', () => {
    const sim = new AirTrafficSimulation();
    const start = process.hrtime.bigint();
    sim.step(1);
    const elapsedMs = Number(process.hrtime.bigint() - start) / 1e6;
    const bruteForcePairs = (500 * 499) / 2;
    assert.ok(sim.engine.stats.preciseChecks < bruteForcePairs / 100,
      `preciseChecks ${sim.engine.stats.preciseChecks} should be << ${bruteForcePairs}`);
    assert.ok(elapsedMs < 1000, `evaluation took ${elapsedMs.toFixed(1)} ms`);
  });
});
