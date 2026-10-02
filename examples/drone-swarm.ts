import { DynamicConvexHullEvolver } from "../src/evolver.ts";
import { polygonArea } from "../src/geometry.ts";

/**
 * Drone swarm scenario: each time slot, a few detection points enter the
 * system with a TTL of 4 slots. Old detections fade out naturally, and the
 * evolver incrementally maintains the safe-airspace boundary (convex hull)
 * of whatever is still alive.
 */

// Deterministic PRNG so the sample output is stable.
let seed = 7;
function random(): number {
  seed |= 0;
  seed = (seed + 0x6d2b79f5) | 0;
  let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
  t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
  return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
}

const TTL = 4;
const SLOTS = 12;
const evolver = new DynamicConvexHullEvolver();
let nextId = 0;

console.log(
  `Drone swarm demo: TTL=${TTL} slots, ${SLOTS} time slots\n` +
    "slot | live | hull vertices (CCW)                         | area",
);
console.log("-".repeat(78));

for (let slot = 0; slot < SLOTS; slot++) {
  // Clock advances first: detections older than TTL fade out here.
  evolver.advanceTo(slot);

  // New detections arrive around a slowly drifting formation center.
  const cx = 20 + slot * 3;
  const cy = 15 + 8 * Math.sin(slot / 2);
  const arrivals = 2 + Math.floor(random() * 3);
  for (let k = 0; k < arrivals; k++) {
    evolver.addPoint(
      {
        id: `uav-${nextId++}`,
        x: Math.round((cx + (random() - 0.5) * 24) * 10) / 10,
        y: Math.round((cy + (random() - 0.5) * 18) * 10) / 10,
      },
      TTL,
    );
  }

  const hull = evolver.getHull();
  const vertices = hull.map((p) => `(${p.x},${p.y})`).join(" ");
  const area = hull.length >= 3 ? polygonArea(hull).toFixed(1) : "0.0";
  console.log(
    `${String(slot).padStart(4)} | ${String(evolver.size).padStart(4)} | ` +
      `${vertices.padEnd(45)} | ${area}`,
  );
}

const s = evolver.stats;
console.log("-".repeat(78));
console.log(
  `stats: ${s.incrementalInserts} incremental inserts, ` +
    `${s.interiorInserts} interior inserts (no hull change), ` +
    `${s.expirations} TTL expirations, only ${s.fullRebuilds} full rebuilds`,
);
