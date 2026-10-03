import { vec3 } from './vec3.ts';
import type { Entity, WarningBeacon } from './engine.ts';
import { CollisionWarningEngine } from './engine.ts';

/** Deterministic PRNG so the demo is reproducible. */
const mulberry32 = (seed: number) => () => {
  seed |= 0;
  seed = (seed + 0x6d2b79f5) | 0;
  let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
  t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
  return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
};

const rand = mulberry32(20261003);

const engine = new CollisionWarningEngine({
  cellSize: 200, // meters per grid cell
  slotDuration: 1, // 1-second time slots
  horizon: 10, // predict 10 seconds ahead
  minSeparation: 50, // 50 m minimum safe distance
  warningLeadTime: 5, // warn 5 seconds before a predicted breach
});

// --- The two tracked airliners: head-on, 200 m/s closing speed. -----------
// Center distance reaches the 60 m breach threshold (50 m + two 5 m radii)
// at exactly t = 10 s.
const entityA: Entity = {
  id: 'A',
  position: vec3(0, 8000, 3000),
  velocity: vec3(100, 0, 0),
  radius: 5,
};
const entityB: Entity = {
  id: 'B',
  position: vec3(2060, 8000, 3000),
  velocity: vec3(-100, 0, 0),
  radius: 5,
};
engine.addEntity(entityA);
engine.addEntity(entityB);

// --- 498 background traffic units on a 9x9x7 lattice, 1.5 km spacing, -----
// cruising in loose formation (small velocity jitter, no conflicts).
let n = 0;
for (let gx = 0; gx < 9 && n < 498; gx++) {
  for (let gy = 0; gy < 9 && n < 498; gy++) {
    for (let gz = 0; gz < 7 && n < 498; gz++) {
      engine.addEntity({
        id: `T${n}`,
        position: vec3(-15000 + gx * 1500, -15000 + gy * 1500, 6000 + gz * 1200),
        velocity: vec3(60 + rand() * 4 - 2, rand() * 4 - 2, rand() * 2 - 1),
        radius: 5,
      });
      n++;
    }
  }
}

console.log(
  `Spacetime collision sentinel: ${engine.entityCount} entities, ` +
    `horizon=${engine.config.horizon}s, minSeparation=${engine.config.minSeparation}m, ` +
    `lead=${engine.config.warningLeadTime}s`,
);

const fmt = (b: WarningBeacon): string =>
  `[BEACON] t=${b.issuedAt.toFixed(1)}s  ${b.entityA} <-> ${b.entityB}  ` +
  `breach in ${b.timeToBreach.toFixed(2)}s (at t=${b.predictedBreachAt.toFixed(2)}s)  ` +
  `current distance ${b.currentDistance.toFixed(1)}m  ` +
  `(min safe ${b.minSeparation}m)`;

// --- Simulate 15 one-second steps. -----------------------------------------
let totalScanMs = 0;
let scans = 0;
for (let step = 0; step <= 15; step++) {
  const start = performance.now();
  const beacons = engine.scan();
  totalScanMs += performance.now() - start;
  scans++;
  for (const b of beacons) console.log(fmt(b));
  if (step < 15) engine.advance(1);
}

const active = engine.activeWarnings();
console.log(`\nActive warnings at t=${engine.currentTime}s: ${active.length}`);
for (const b of active) console.log(`  ${fmt(b)}`);
console.log(
  `Avg scan time over ${scans} scans with ${engine.entityCount} entities: ` +
    `${(totalScanMs / scans).toFixed(2)} ms`,
);
