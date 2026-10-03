import { CollisionWarningEngine, TrackedEntity, WarningBeacon } from './engine';
import { add, scale } from './geometry';

/** Deterministic PRNG (mulberry32) so scenarios are reproducible. */
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/**
 * Build the acceptance scenario: 500 air-traffic units, two of which
 * ("A" and "B") fly head-on converging courses that breach the 50 m
 * minimum safe separation exactly 9.5 s after scenario start.
 *
 * A starts at x=0 moving +50 m/s, B at x=1020 moving -50 m/s, both with
 * 10 m body radius. Center distance closes at 100 m/s; the surface gap
 * reaches 50 m (center distance 70 m) at t = (1020 - 70) / 100 = 9.5 s.
 */
export function buildScenario(seed = 20261003, unitCount = 500): TrackedEntity[] {
  const rand = mulberry32(seed);
  const units: TrackedEntity[] = [];

  units.push({
    id: 'A',
    position: { x: 0, y: 0, z: 1000 },
    velocity: { x: 50, y: 0, z: 0 },
    radius: 10,
  });
  units.push({
    id: 'B',
    position: { x: 1020, y: 0, z: 1000 },
    velocity: { x: -50, y: 0, z: 0 },
    radius: 10,
  });

  for (let i = 2; i < unitCount; i++) {
    units.push({
      id: 'U' + i,
      position: {
        x: 5000 + rand() * 40000,
        y: 5000 + rand() * 40000,
        z: 500 + rand() * 10000,
      },
      velocity: {
        x: (rand() * 2 - 1) * 150,
        y: (rand() * 2 - 1) * 150,
        z: (rand() * 2 - 1) * 25,
      },
      radius: 8 + rand() * 6,
    });
  }
  return units;
}

/** Steps 500 units through time and evaluates the warning engine each tick. */
export class AirTrafficSimulation {
  readonly units: TrackedEntity[];
  readonly engine: CollisionWarningEngine;
  time = 0;

  constructor(seed = 20261003, unitCount = 500) {
    this.units = buildScenario(seed, unitCount);
    this.engine = new CollisionWarningEngine({
      minSeparation: 50,
      slotDuration: 1,
      horizonSlots: 10,
      cellSize: 256,
    });
  }

  /** Advance one tick; returns beacons newly raised at the current time. */
  step(dt = 1): WarningBeacon[] {
    const beacons = this.engine.evaluate(this.units, this.time);
    for (const u of this.units) {
      u.position = add(u.position, scale(u.velocity, dt));
    }
    this.time += dt;
    return beacons;
  }
}
